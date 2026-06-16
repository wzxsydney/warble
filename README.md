# warble

![license: MIT](https://img.shields.io/badge/license-MIT-blue)
![platform: Linux | macOS](https://img.shields.io/badge/platform-Linux%20%7C%20macOS-lightgrey)
![status: beta](https://img.shields.io/badge/status-beta-orange)

A fully local AI voice backend for the [M5Stack StackChan](https://docs.m5stack.com/en/stackchan)
desktop robot. It replaces the `xiaozhi.me` cloud so the robot hears, thinks, and
speaks entirely on your own hardware: no cloud account, no API keys, no audio
leaving your network.

The stock StackChan firmware (the `xiaozhi-esp32` AI agent) connects to a cloud
backend for speech-to-text, the language model, and text-to-speech. warble
implements that same protocol locally, so you point the robot at your machine and
nothing else changes on the device. It handles the full voice conversation
(listen, transcribe, reply, speak) and drives the robot's animated face.

```
StackChan robot (CoreS3, xiaozhi firmware)
      |
      |  Wi-Fi / WebSocket: you speak (audio up), it replies (audio + face down)
      v
warble server (Go)  -  WebSocket :8000, OTA :8003
      |
      +--> whisper.cpp   speech-to-text   :8082
      +--> Silero VAD    voice detection  :8005
      +--> Piper         text-to-speech   :8001
      +--> Ollama        language model   :11434
```

The server runs the whole turn locally: whisper.cpp transcribes what you say,
Silero VAD detects when you stop, Ollama generates the reply, and Piper speaks it.
The server also sets the robot's face from an emotion tag in each reply.

![A live conversation viewed with the warble transcript command: each line shows what the robot heard and replied, tagged with a mood that drives its face](docs/transcript.png)

*A real conversation captured with `./warble transcript`. What it heard, the reply, and the mood driving the robot's face all ran on a local machine. No cloud, no account.*

> **New here?** Follow the step-by-step [Getting Started guide](docs/getting-started.md).
> It walks through everything below in plain language.

## What you need

warble runs on **Linux or macOS**. (Windows is not supported: it's bash-based and
talks to Unix serial ports for flashing the robot.)

- An M5Stack StackChan (CoreS3) running its `xiaozhi-esp32` AI-agent firmware.
- A Linux or macOS computer on the same Wi-Fi/LAN to run warble, with Docker
  installed and running (Docker Engine on Linux, Docker Desktop on macOS).
- Room for the AI model. A small model like `gemma3:4b` runs on a modest machine;
  bigger models want more RAM/VRAM and ideally a GPU. On macOS, run Ollama natively
  for GPU acceleration (the setup script handles this).
- Disk for models: whisper from ~150 MB (`base`) to ~3 GB (`large-v3`), a Piper
  voice (~60 MB), and the AI model.

## Quick start

One command does everything: checks Docker (and offers to install it), fetches
models, configures, builds, starts, and tells you when it's ready.

```bash
git clone https://github.com/rebelthor/warble
cd warble
./warble start
```

At the end it offers to point your robot at this computer (plug it in via USB and
confirm). Then open the AI Agent app on the robot and talk. You can also do this
step later, or redo it, any time:

```bash
./warble connect       # point the robot at this computer
./warble disconnect    # send the robot back to the M5Stack cloud
```

Pointing the robot reprograms it over USB and needs `esptool` on this computer
(`pipx install esptool`, or `pip install esptool`).

## Everyday use

One command, a few verbs:

```bash
./warble status        # plain-language health check (running? ready?)
./warble transcript    # live conversation: what it heard (USER) and replied (BOT)
./warble logs server   # raw server logs (or ./warble logs for everything)
./warble stop          # stop everything
./warble start         # start again (safe to re-run any time)
./warble restart       # bounce the running services
```

If `./warble status` says nothing is running, make sure **Docker is running**
(on macOS, open Docker Desktop; it does not auto-start after a reboot), then run
`./warble start` again.

## Configuration

Two layers, split by how often they change.

**Live** (edit the file, takes effect within ~1 second, no restart):

| File | What |
|---|---|
| `server/config/prompt.txt` | The robot's personality / instructions. Keep the emotion-tag line (see below). |
| `server/config/runtime.json` | `piper_voice`, `ollama_model`, and sampling (`temperature`, `top_p`, `num_predict`). |

**Startup** (set in `.env`, then `./warble start` to apply): the AI model location,
whisper model + language, the Piper voice. See `.env.example`.

### Use a different language

warble is language-neutral; three settings pick the language:

1. **whisper** model + `WHISPER_LANG` in `.env` (e.g. `de`, `ro`, or `auto`).
2. **Piper voice**, fetch one for your language (browse
   [piper voices](https://huggingface.co/rhasspy/piper-voices)) and set `PIPER_VOICE`.
3. **Prompt**, write `server/config/prompt.txt` in your language.

### The emotion tag

Every reply begins with one tag from
`[neutral|happy|laughing|angry|sad|crying|doubtful]`. The server removes it before
speaking and sends a matching face to the robot. It is a firmware requirement, not
decoration: keep that instruction in any custom prompt (`prompt.example.txt` shows
the exact wording). If the model omits it, the server uses `neutral`.

## Advanced

- **Prebuilt images.** `./warble start` pulls prebuilt images from
  `ghcr.io/rebelthor/warble-*` (fast) and falls back to building locally if they
  can't be pulled. Force a local build with `./warble start --build`. Pin a version
  with `WARBLE_VERSION` in `.env`. Publishing images: see [`docs/releasing.md`](docs/releasing.md).
- **Manual Docker control** and the macOS/Linux Ollama details: see the comments
  in `docker-compose.yml` and `.env.example`.
- **Run without Docker** (install whisper.cpp / Piper / Ollama yourself and use the
  `bin/warble` supervisor): see [`docs/operations.md`](docs/operations.md).
- **Protocol details**: [`server/PROTOCOL.md`](server/PROTOCOL.md).

## Components and licenses

warble's own code is MIT. It orchestrates external programs you install; see
[`THIRD_PARTY_LICENSES.md`](THIRD_PARTY_LICENSES.md). One boundary matters:
**Piper (`piper-tts`) is GPL-3.0**. warble talks to it over HTTP, so warble stays
MIT. The prebuilt `warble-piper` image bundles Piper, so that image is distributed
under GPL-3.0 (labeled accordingly; its corresponding source is
`piper-shim/Dockerfile` plus upstream `piper1-gpl`). The other three images
(`warble-server`, `warble-whisper`, `warble-vad`) are MIT.

Protocol implemented from the [`xiaozhi-esp32`](https://github.com/78/xiaozhi-esp32)
docs ([WebSocket](https://github.com/78/xiaozhi-esp32/blob/main/docs/websocket.md)).

## Contributing

Issues and PRs welcome. See [`CONTRIBUTING.md`](CONTRIBUTING.md).
