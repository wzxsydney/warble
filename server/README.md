# server

Go AI server inside the warble stack. Implements the xiaozhi WebSocket + OTA protocol for the StackChan device. Single ~10 MB binary, ~1.5k LOC.

- Wire spec: [PROTOCOL.md](PROTOCOL.md)

## Layout

```
cmd/
  server/         server binary (HTTP :8003 + WS :8000)
  testclient/     wire-level test harness (-scenario full|barge|reconnect|idle)
internal/
  audio/          opus encode/decode, WAV codec, linear resampler
  asr/            Whisper multipart POST → text
  llm/            Ollama /api/chat streaming + non-streaming
  tts/            piper-shim POST → WAV → PCM
  pipeline/       sentence splitter, audio pacer, ASR/LLM/TTS orchestration, emotion-tag parser
  vad/            Silero VAD WS client (server-side end-of-speech)
  ws/             WS server, per-session state machine, audioEvent multiplexer, session registry
  ota/            POST /xiaozhi/ota/ handler
  debug/          /debug/sessions + /debug/say (mounted only with WARBLE_DEBUG=1)
  config/         env-var overrides
  proto/          JSON message types matching firmware
```

## Build and run

The recommended path is through the sidecar manager, which builds + supervises every dependency (whisper, piper, vad, ollama, server):

```bash
bin/warble start
```

Direct run (assumes sidecars are already up):

```bash
cd server
go build -o bin/server ./cmd/server
./bin/server
```

JSON logs go to stdout. Set `WARBLE_DEBUG=1` for verbose per-frame logs.

## Test scenarios

```bash
go build -o bin/testclient ./cmd/testclient

# Smoke (no audio decoded server-side, no Whisper/Ollama/Piper hit)
./bin/testclient -fixture wire

# End-to-end with a 16k mono PCM WAV
./bin/testclient -fixture /tmp/fixture_ro.wav -scenario full

# Barge-in: abort after N audio frames
./bin/testclient -fixture /tmp/fixture_ro.wav -scenario barge -barge-after 5

# Reconnect: two consecutive turns over fresh WS
./bin/testclient -fixture /tmp/fixture_ro.wav -scenario reconnect

# Idle disconnect: hello + wait ~120s
./bin/testclient -scenario idle
```

Generate a fresh fixture from the running piper-shim:

```bash
curl -sf -X POST http://127.0.0.1:8001/v1/audio/speech \
  -H 'Content-Type: application/json' \
  -d '{"input":"Hello, what is your name?","voice":"en_US-lessac-medium"}' \
  -o /tmp/fixture.wav
```

## Configuration

Settings split by reload behavior:

- **Hot-reloadable** (edit the file, takes effect within ~1 s, no restart): `config/prompt.txt` and `config/runtime.json`. See [Hot-reloadable config](#hot-reloadable-config) below.
- **Cold-only** (env vars, restart required): ports, sidecar URLs, audio frame geometry, VAD tuning. Listed in the table below.

### Cold-only env vars

| Var | Default | Purpose |
|---|---|---|
| `WARBLE_WS_URL` | (auto) | Force a specific `websocket.url` in OTA replies |
| `WARBLE_WHISPER_URL` | `http://127.0.0.1:8082/inference` | whisper-server endpoint |
| `WARBLE_OLLAMA_URL` | `http://127.0.0.1:11434` | Ollama base |
| `WARBLE_PIPER_URL` | `http://127.0.0.1:8001/v1/audio/speech` | piper-shim endpoint |
| `WARBLE_CONFIG_DIR` | `./config` | dir holding `prompt.txt` + `runtime.json` |
| `WARBLE_VAD_URL` | `ws://127.0.0.1:8005/vad` | Silero VAD sidecar (empty disables server-side EOS) |
| `WARBLE_DEBUG` | unset | `1` flips slog to Debug AND mounts the `/debug/*` namespace on `:8003` |

### Hot-reloadable config

`config/prompt.txt` — plain text system prompt. Edit freely; trailing newlines trimmed. Empty file is rejected and the previous snapshot is kept.

The prompt instructs the model to lead every reply with a bracketed emotion tag — one of `[neutral|happy|laughing|angry|sad|crying|doubtful]`. The pipeline parses and strips this tag before TTS, then emits an `{"type":"llm","emotion":"..."}` WS frame so the device face updates roughly in sync with audio start. Tag set matches the firmware's `StackChanAvatarDisplay::SetEmotion` (`stackchan_display.cc:329-365`). `sleepy` is excluded because it triggers a "Zzz…" overlay + halts idle motion. See [LOCAL_BACKEND.md](../../LOCAL_BACKEND.md) § Face emotion pipeline for the full flow.

`config/runtime.json` — model + voice + sampling:

```json
{
  "piper_voice": "en_US-lessac-medium",
  "ollama_model": "gemma3:4b",
  "temperature": 0.6,
  "top_p": 0.9,
  "num_predict": 80
}
```

A background poller stats both files every ~1 s. On mtime advance, the file is re-read and an atomic snapshot swap happens. Each turn pins one snapshot at the top of `runTurn`, so a reload mid-turn cannot split prompt / model / voice across the same exchange.

When the prompt or model changes the loader fires a background `Warmup` so the next turn doesn't pay the prefix-cache miss (~500 ms in practice with the model already loaded). Voice and sampling changes apply on the next turn without rewarm.

Validation errors (missing file, bad JSON, empty prompt, missing `piper_voice` / `ollama_model`) log a warning and keep the previous snapshot — they don't crash a running session.

Watch the reload events in `server.log`:

```bash
warble/bin/warble logs server | grep -E "config\.(loaded|reloaded|rewarm)"
```

## Debug endpoints (`WARBLE_DEBUG=1`)

Mounted on the OTA listener (`:8003`) when the env flag is set. Off by default so the LAN can't drive the robot's speaker without intent.

```bash
# List live WS sessions
curl http://127.0.0.1:8003/debug/sessions

# Inject a text turn into the active session (skips ASR; runs LLM + TTS + face)
curl -X POST http://127.0.0.1:8003/debug/say \
  -H 'Content-Type: application/json' \
  -d '{"text":"tell me a short joke"}'
```

If exactly one session is live, `device_id` can be omitted. Otherwise pass `{"device_id":"aa:bb:cc:dd:ee:ff","text":"..."}`.

The CLI wrapper is `warble say "..."` / `warble sessions`. Implementation in `internal/debug/`. The injected turn rides the same `audioEvents` channel as real audio, so it serializes with any real turn through the per-session `turnLoop` goroutine — no concurrency surprises.

## Behavior and scope

- **One global system prompt**, hot-reloadable (edit `config/prompt.txt`). Prompts are not per-device.
- **Conversation history is in-process**, cleared on restart.
- **`realtime` listen mode is treated as `auto`.**
- **Handles voice conversation and the face.** MCP, IoT control, music, and function-calling are not part of it.
