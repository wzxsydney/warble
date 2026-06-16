# Bare-metal operations (`bin/warble`)

`bin/warble` supervises the local processes when you are not using Docker. It
reads `warble.env` (copy `warble.env.example`). The Docker path does not use it.

```bash
bin/warble status              # show all services
bin/warble start all           # whisper + piper + vad + server (+ ollama per OLLAMA_MODE)
bin/warble start server        # one service
bin/warble stop all            # tear down
bin/warble restart server
bin/warble logs                # interleaved colored tail of all logs
bin/warble logs server         # tail one service's log
bin/warble transcript          # conversation view, mood chip per reply
bin/warble sessions            # list live WS sessions   (needs WARBLE_DEBUG=1)
bin/warble say "text..."       # inject a turn — hands-free testing (needs WARBLE_DEBUG=1)
bin/warble babysit server      # poll every 10s, restart if dead
```

## Setup (Python sidecars)

Create the two venvs from the pinned requirements, then fetch models:

```bash
python3 -m venv envs/piper && envs/piper/bin/pip install -r piper-shim/requirements.txt
python3 -m venv envs/vad   && envs/vad/bin/pip   install -r vad-shim/requirements.txt
scripts/fetch-models.sh
```

`warble.env` points `PIPER_UVICORN` / `VAD_UVICORN` at these venvs by default. If
you move the project tree, recreate the venvs (don't try to patch the absolute
shebangs in `envs/*/bin/`). Set the launchers to a bare `uvicorn` to use a system
or conda environment instead.

Install whisper.cpp's `whisper-server` and Ollama yourself (or run Ollama in
Docker / natively and set `OLLAMA_MODE=external`).

## Ollama modes (`OLLAMA_MODE`)

- `external` (default) — you run Ollama; `bin/warble` only health-checks `:11434`.
- `serve` — `bin/warble` runs `ollama serve` as a child process.
- `brew` — macOS Homebrew: drives `brew services start/stop ollama`.

## Status states and colors

```
service   process               port    health
whisper   running(19037)        :8082   ok
ollama    running(19332)        :11434  ok
```

Each service has a consistent identity color (status row, log prefix, start/stop
output). The process-state colors are independent:

| Process column | Meaning |
|---|---|
| `running(PID)` (green) | up. For directly-spawned services the pidfile PID equals the port listener. For `ollama`, the PID is whatever process is bound to its port. |
| `orphan(PID)` (yellow) | directly-spawned service with no live pidfile but something bound to the port. `start` adopts it; `stop` kills it. |
| `stopped` (red) | nothing listening on the port. |

The health column runs a real HTTP probe, independent of the pidfile, so an
`orphan` row can still show a green port and `ok` health.

## Debug helpers (`WARBLE_DEBUG=1`)

`sessions` and `say` hit `/debug/sessions` and `/debug/say` on the OTA port,
mounted only when the server is started with `WARBLE_DEBUG=1`. `say` skips ASR but
runs the full LLM + TTS + emotion pipeline against the active device session.

The device's WebSocket only stays open during an active turn, so the inject
window is a few seconds per interaction. Trigger the device (wake word or screen
tap) first, then fire `say` immediately, or use `say --wait` to block until a
session appears.

## Device serial — `bin/serial`

Helper for reading the device's USB serial log. The CoreS3 enumerates as
`/dev/cu.usbmodem*` on macOS and `/dev/ttyACM*` on Linux. Set `STACKCHAN_PORT`
to override.

```bash
bin/serial port      # show detected port
bin/serial tail      # foreground stream
bin/serial bg        # background capture to a log file
bin/serial watch     # tail -F the backgrounded log
bin/serial stop      # stop the tailer, free the port
bin/serial monitor   # esp-idf-monitor with panic decode + auto-reconnect
```

Note: Wi-Fi credentials are logged in plaintext by the firmware during BLE
config. Treat captured serial logs as sensitive.

## Common workflows

Bring the stack up cold (Ollama already running):

```bash
bin/warble start all
bin/warble status
```

Diagnose "robot listens but never replies":

```bash
bin/warble status            # all green?
tail -50 log/vad.log         # VAD model loaded?
tail -50 log/server.log      # speech_end events? turn.start?
```

Debug a turn without speaking to the robot:

```bash
WARBLE_DEBUG=1 bin/warble restart server
bin/warble transcript &      # watch USER/BOT lines
# trigger the device, then:
bin/warble say "tell me a short joke"
```
