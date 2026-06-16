# xiaozhi Wire Protocol — extracted spec

Canonical spec for what the vendored `xiaozhi-esp32` firmware client requires from the server. Every firmware claim cites a line in upstream `78/xiaozhi-esp32@v2.2.4`. This is what the `server` implements.

Path convention in citations:
- `fw/...` = `78/xiaozhi-esp32/blob/v2.2.4/main/...` on GitHub

## Transport

WebSocket only. MQTT branch (`fw/protocols/mqtt_protocol.cc`) is not in scope — the firmware picks WebSocket when the OTA response contains a `websocket` block but no `mqtt` block ([fw/ota.cc:146-186](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/ota.cc#L146)).

Two server-side ports:

| Port | Protocol | Path           | Purpose                              |
|------|----------|----------------|--------------------------------------|
| 8003 | HTTP/1.1 | `/xiaozhi/ota/`| Bootstrap, returns WS URL + clock    |
| 8000 | WS       | `/xiaozhi/v1/` | Audio session                        |

The device picks these up from whatever URL is in NVS namespace `wifi:ota_url` ([fw/ota.cc:46-53](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/ota.cc#L46)) and whatever WS URL the OTA response gave it.

## OTA endpoint

### Request

```http
POST /xiaozhi/ota/ HTTP/1.1
Content-Type: application/json
User-Agent: <board>/<version>
Accept-Language: <code>
Device-Id: aa:bb:cc:dd:ee:ff
Client-Id: <uuid>
Activation-Version: 1|2
Serial-Number: <32 chars, only if Activation-Version=2>

{ "version": 2, "language": "en-US", "flash_size": 16777216, ... board JSON ... }
```

Headers set at [fw/ota.cc:55-71](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/ota.cc#L55). Body is `board.GetSystemInfoJson()` ([fw/ota.cc:93](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/ota.cc#L93)) — opaque to the server. If body is empty, the firmware uses GET; ignore that path.

### Response

Minimum the firmware parses:

```json
{
  "server_time": {
    "timestamp": 1715432913000,
    "timezone_offset": 180
  },
  "firmware": {
    "version": "<echo of device-reported version>",
    "url": ""
  },
  "websocket": {
    "url": "ws://<host>:8000/xiaozhi/v1/",
    "token": ""
  }
}
```

Field-by-field semantics, all from [fw/ota.cc:114-241](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/ota.cc#L114):

- `server_time.timestamp` (number, ms since epoch). Used to set device clock via `settimeofday()` after adding `timezone_offset * 60_000` ms ([fw/ota.cc:194-208](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/ota.cc#L194)).
- `server_time.timezone_offset` (number, minutes). Required for clock to be local time.
- `firmware.version` (string). Compared against running version ([fw/ota.cc:225-232](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/ota.cc#L225)). If higher and `firmware.url` is non-empty, device downloads + reboots. We always echo the device's own version so no update is triggered.
- `firmware.url` (string). Set to `""`. We do not serve firmware.
- `firmware.force` (number, optional). `1` forces update regardless of version. Never set.
- `websocket.url` (string). Persisted to NVS `websocket:url` ([fw/ota.cc:170-182](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/ota.cc#L170)). Read by WS client at [fw/protocols/websocket_protocol.cc:84-85](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/websocket_protocol.cc#L84).
- `websocket.token` (string). Persisted to NVS `websocket:token`. Sent as `Authorization: Bearer <token>` ([fw/protocols/websocket_protocol.cc:101-107](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/websocket_protocol.cc#L101)). We send `""` and the firmware skips the header.
- `websocket.version` (number, optional). Persisted to NVS `websocket:version`. Controls binary framing. We omit it; firmware default is the compiled-in `version_` (typically 1 = raw opus). See "Binary framing" below.

Optional blocks the firmware also reads but we omit:

- `activation.{message, code, challenge, timeout_ms}` — registration flow with Tenclass. Absent block = device already activated.
- `mqtt.*` — alternate transport. Omitting the block forces WebSocket.

### GET handler

The firmware uses GET if its system-info body is empty. A plain-text status string is accepted.

### Response time budget

No explicit deadline in code, but the firmware's HTTP client retries on failure ([fw/ota.cc:97-101](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/ota.cc#L97)). Keep response under 1 s.

## WebSocket session

### Connection headers (client → server)

Set at [fw/protocols/websocket_protocol.cc:101-110](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/websocket_protocol.cc#L101):

| Header           | Value                                     | Required |
|------------------|-------------------------------------------|----------|
| Authorization    | `Bearer <token>` (only if token non-empty)| optional |
| Protocol-Version | string, "1" / "2" / "3"                   | yes      |
| Device-Id        | MAC address, lowercase colon-separated    | yes      |
| Client-Id        | UUID4 string from `board.GetUuid()`       | yes      |

### `hello` handshake

Device sends first, within ~immediate of WS open ([fw/protocols/websocket_protocol.cc:182-186](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/websocket_protocol.cc#L182)):

```json
{
  "type": "hello",
  "version": 1,
  "features": { "mcp": true },
  "transport": "websocket",
  "audio_params": {
    "format": "opus",
    "sample_rate": 16000,
    "channels": 1,
    "frame_duration": 60
  }
}
```

Built in [fw/protocols/websocket_protocol.cc:203-226](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/websocket_protocol.cc#L203). `features.aec` only present if `CONFIG_USE_SERVER_AEC` was set at firmware build time (it isn't, in our build).

Server must reply within **10 seconds** or device errors ([fw/protocols/websocket_protocol.cc:189-194](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/websocket_protocol.cc#L189)):

```json
{
  "type": "hello",
  "transport": "websocket",
  "session_id": "<uuid>",
  "audio_params": {
    "sample_rate": 24000,
    "frame_duration": 60
  }
}
```

Server reply parsed in [fw/protocols/websocket_protocol.cc:228-254](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/websocket_protocol.cc#L228):

- `transport`: must be `"websocket"`, else device logs error and bails ([fw/.../websocket_protocol.cc:229-233](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/websocket_protocol.cc#L229)).
- `session_id`: stored, included in every subsequent text frame the device sends.
- `audio_params.sample_rate`: number, configures device's opus decoder for downstream audio. Default if absent: 24000.
- `audio_params.frame_duration`: number, ms. Default 60.

Values in use: 24 kHz, 60 ms.

For emotion display, don't negotiate MCP back — leave `features` out of the reply. The device only initializes its MCP server when the server acknowledges by routing `mcp` messages back; the server doesn't.

### Client → server messages (after hello)

All carry `session_id` (matching the one we issued) and a `type`.

#### `listen` — state machine driver

Three states: `start`, `stop`, `detect`. Sent at [fw/protocols/protocol.cc:51-74](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/protocol.cc#L51).

```json
{ "session_id": "...", "type": "listen", "state": "start", "mode": "auto|manual|realtime" }
{ "session_id": "...", "type": "listen", "state": "stop" }
{ "session_id": "...", "type": "listen", "state": "detect", "text": "<wake phrase>" }
```

- `mode`:
  - `auto` (`kListeningModeAutoStop`): VAD on device decides when user stopped speaking. Server can stop listening at any time.
  - `manual` (`kListeningModeManualStop`): user holds a button. Device sends `stop` when released.
  - `realtime` (`kListeningModeRealtime`): continuous streaming with AEC. We won't support; map to `auto`.

- `detect` is sent when the on-device wake word fires. `text` is the wake phrase. Server should treat as if `start` had just been received and there's a transcript already.

Triggered on device when entering listening state ([fw/application.cc:889-894](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/application.cc#L889)).

#### `abort` — user interrupted

```json
{ "session_id": "...", "type": "abort" }
{ "session_id": "...", "type": "abort", "reason": "wake_word_detected" }
```

[fw/protocols/protocol.cc:42-49](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/protocol.cc#L42). Server must:

1. Cancel in-flight LLM/TTS work.
2. Flush pending audio packets.
3. Send `{"type":"tts","state":"stop","session_id":"..."}`.
4. Drop server-side speak-state flags.

#### `mcp` — IoT control passthrough

```json
{ "session_id": "...", "type": "mcp", "payload": { ...arbitrary JSON-RPC... } }
```

[fw/protocols/protocol.cc:76-79](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/protocol.cc#L76). Only sent if server-side MCP was negotiated. We skip MCP, so we should never see these. If we do, log and drop.

#### Binary frames — opus audio upstream

Format depends on the negotiated version (NVS `websocket:version`). Three variants at [fw/protocols/websocket_protocol.cc:33-58](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/websocket_protocol.cc#L33):

**Version 1 (raw opus, default when NVS key absent)**

```
[ opus bytes ]
```

One opus packet per frame, 60 ms each, 16 kHz mono. ~120 bytes typical at 16 kbps. We pin v1.

**Version 2 (16-byte header, network byte order)**

```c
struct BinaryProtocol2 {
    uint16_t version;        // 2
    uint16_t type;           // 0 = OPUS, 1 = JSON
    uint32_t reserved;
    uint32_t timestamp;      // ms, used for server-side AEC
    uint32_t payload_size;
    uint8_t  payload[];
} __attribute__((packed));
```

[fw/protocols/protocol.h:17-24](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/protocol.h#L17). Only if AEC is enabled in firmware build. Not our case.

**Version 3 (4-byte header)**

```c
struct BinaryProtocol3 {
    uint8_t  type;
    uint8_t  reserved;
    uint16_t payload_size;   // network byte order
    uint8_t  payload[];
} __attribute__((packed));
```

[fw/protocols/protocol.h:26-31](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/protocol.h#L26). Not used by our firmware build.

### Server → client messages

All text frames; binary frames are opus only.

#### `tts` — drive device speak state

```json
{ "type": "tts", "state": "start",          "session_id": "..." }
{ "type": "tts", "state": "sentence_start", "session_id": "...", "text": "..." }
{ "type": "tts", "state": "stop",           "session_id": "..." }
```

Handled at [fw/application.cc:524-549](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/application.cc#L524):

- `start`: device transitions to `kDeviceStateSpeaking`, sets `aborted_ = false`.
- `sentence_start`: text shown on display as assistant message (does NOT change device state).
- `stop`: device transitions back. If `listening_mode_ == kListeningModeManualStop`, goes to `kDeviceStateIdle`; otherwise back to `kDeviceStateListening` (auto re-listens).

Server ordering for one user turn:

```
{stt, text: "<user transcript>"}                       ← optional but expected
{tts, state: start}
{tts, state: sentence_start, text: "first sentence"}
<binary opus frames for first sentence>
{tts, state: sentence_start, text: "second sentence"}
<binary opus frames for second sentence>
...
{tts, state: stop}
```

Each `sentence_start` frame must precede the audio for that sentence. Device renders the text immediately.

#### `stt` — show user transcript

```json
{ "type": "stt", "text": "what the user said", "session_id": "..." }
```

[fw/application.cc:550-557](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/application.cc#L550). Sent once per user turn, after ASR completes, before `tts:start`.

#### `llm` — emotion tag

```json
{ "type": "llm", "emotion": "neutral|happy|sad|angry|confused|...", "text": "" }
```

[fw/application.cc:558-564](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/application.cc#L558). Sets the face expression. Optional. Phase 2.

#### `alert` — error popup with sound

```json
{ "type": "alert", "status": "...", "message": "...", "emotion": "..." }
```

[fw/application.cc:583-591](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/application.cc#L583). All three fields required (string), else device logs warning and drops. Plays `Lang::Sounds::OGG_VIBRATION` and shows the message. Useful for surfacing "LLM unreachable" etc.

#### `system` — only `reboot` is honored

```json
{ "type": "system", "command": "reboot" }
```

[fw/application.cc:570-582](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/application.cc#L570). Out of scope; do not send.

#### Binary frames — opus audio downstream

Match the version used upstream. For version 1: raw opus, frame_duration 60 ms, sample_rate as advertised in our `hello` reply (24 kHz default).

## Device state machine (for server's mental model)

From [fw/device_state.h](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/device_state.h) referenced in `application.cc`:

```
            ┌─────────────────────────────────────┐
            │                                     │
            ▼          listen.start ──►           │
        ┌────────┐                          ┌──────────┐
        │  Idle  │                          │ Listening│
        └────────┘  ◄── listen.stop ──      └──────────┘
            ▲          (manual mode)              │
            │                                     │ tts.start
            │  tts.stop (manual)                  ▼
            │                                ┌──────────┐
            │                                │ Speaking │
            │  tts.stop (auto, returns to Listening)
            └──── tts.stop (auto) ─────  ────└──────────┘
```

Server is responsible for sending `tts.stop` after audio drains. The server waits for the audio queue to empty plus a `(PRE_BUFFER_COUNT + 2) * frame_duration` slack (~420 ms) to account for client-side jitter buffer. See [internal/pipeline/pacer.go](internal/pipeline/pacer.go).

## Timing constants

| Bound | Value | Source | Action if violated |
|-------|-------|--------|---------------------|
| Hello reply | 10 s | [fw/.../websocket_protocol.cc:189](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/websocket_protocol.cc#L189) | Device closes WS |
| Server → device idle | 120 s | [fw/protocols/protocol.cc:82](https://github.com/78/xiaozhi-esp32/blob/v2.2.4/main/protocols/protocol.cc#L82) | Device closes WS |
| Server-side no-voice | 120 s | server session timeout | Server says goodbye, closes |

Server's keep-alive: send any frame (e.g. periodic empty `system` is NOT honored — keep an internal heartbeat by repeating last `tts:stop` is overkill). Easiest: as long as a session is active and there's no voice, just let the 120 s timeout happen and let the device reconnect on the next wake.

## Audio pacing — what the device tolerates

The device's opus decoder feeds a small jitter buffer. Sending all frames as fast as possible **overruns** that buffer (drops); sending slower than wall-clock **underruns** (gaps and glitches). The server's strategy in [internal/pipeline/pacer.go](internal/pipeline/pacer.go):

1. **Prebuffer phase**: send first 5 frames immediately (`PRE_BUFFER_COUNT = 5`). That's 300 ms of headroom at 60 ms/frame.
2. **Pacing phase**: maintain a virtual `play_position` that advances by `frame_duration` ms per packet. Before sending packet N, compute `elapsed_ms = monotonic_now - start`. If `elapsed_ms < play_position`, sleep the difference. Otherwise send immediately (catching up).
3. **Drain**: after the last packet, wait for the queue to empty, then sleep `(PRE_BUFFER_COUNT + 2) * frame_duration` ms = 420 ms before sending `tts:stop`.

These numbers come from device-side empirical testing — changing them risks audible artifacts.

## Session lifecycle summary

```
Boot / app wakes Wi-Fi
   │
   ▼
POST /xiaozhi/ota/  ←─────────────────── one-shot, returns ws_url
   │
   ▼
WS connect to ws_url
   │
   ▼
device → {type:hello, ...}
server ← {type:hello, session_id, audio_params}     ← within 10 s
   │
   ▼
(idle, no traffic)
   │
   ▼  device wakeword or button press
device → {type:listen, state:start, mode:auto}
device → <opus frames>...
device → {type:listen, state:stop}                  ← in auto mode, or manual release
   │
   ▼
server runs VAD/ASR/LLM/TTS
server → {type:stt, text:"..."}
server → {type:tts, state:start}
server → {type:tts, state:sentence_start, text:"..."}
server → <opus frames for sentence 1>
server → {type:tts, state:sentence_start, text:"..."}
server → <opus frames for sentence 2>
...
server → {type:tts, state:stop}
   │
   ▼
device returns to listening (auto) or idle (manual)
   │
   ▼  120 s no voice
device or server closes WS
```

## What we explicitly do NOT implement

- `mqtt` block in OTA response (alternate transport)
- `activation` block in OTA response
- `firmware.url` non-empty (OTA delivery)
- Server-side AEC (`features.aec`)
- `mcp` message routing
- `system: reboot` command
- Authentication tokens
- Wake-word response cache (`wakeup_words` audio prerendering)
- Device-binding flow (`need_bind`)
- Multi-tenant config / per-device prompts
- Chat history persistence
- Vision endpoint (`/mcp/vision/explain`)
- Music playback
- Function calling / intent classification
