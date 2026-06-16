# Third-party components

warble's own code is MIT (see `LICENSE`). It orchestrates several external
programs and models that you install yourself; with one exception (the prebuilt
`warble-piper` image, see the boundary note below) warble does not redistribute
their code or binaries. Their licenses are theirs. This file credits them and
flags the one license boundary that matters for redistribution.

| Component | Role in warble | License | Notes |
|---|---|---|---|
| [xiaozhi-esp32](https://github.com/78/xiaozhi-esp32) | Device firmware + the WebSocket/OTA protocol warble implements | MIT | warble speaks its protocol; no code vendored. Protocol docs: [`websocket.md`](https://github.com/78/xiaozhi-esp32/blob/main/docs/websocket.md), [`mcp-protocol.md`](https://github.com/78/xiaozhi-esp32/blob/main/docs/mcp-protocol.md) |
| [whisper.cpp](https://github.com/ggml-org/whisper.cpp) | Speech-to-text (`whisper-server`) | MIT | Compiled from a pinned source tag in warble's whisper image (the official image ships the CLI, not the server) |
| [Silero VAD](https://github.com/snakers4/silero-vad) | Voice activity detection (model + `silero-vad` pip package) | MIT | |
| [Piper](https://github.com/OHF-Voice/piper1-gpl) (`piper-tts`) | Text-to-speech engine | **GPL-3.0** | See the boundary note below |
| [Piper voices](https://huggingface.co/rhasspy/piper-voices) | TTS voice models (`*.onnx`) | MIT (per-voice; check the model card) | Downloaded by the user |
| [Ollama](https://github.com/ollama/ollama) | LLM runtime | MIT | Installed/run by the user |
| LLM weights (e.g. Gemma) | The language model Ollama serves | Model-specific (e.g. the Gemma Terms of Use) | You choose and pull the model; review its terms |

## Go dependencies (all permissive)

- [`github.com/coder/websocket`](https://github.com/coder/websocket), ISC
- [`github.com/google/uuid`](https://github.com/google/uuid), BSD-3-Clause
- [`gopkg.in/hraban/opus.v2`](https://github.com/hraban/opus), MIT (wraps libopus, BSD-3-Clause)

## The Piper GPL boundary (read before publishing a Docker image)

Piper (`piper-tts`, the OHF-Voice/piper1-gpl engine) is GPL-3.0, because it
links espeak-ng. warble's `piper-shim` talks to Piper as a **separate program
over HTTP**, so warble's own code stays MIT. That arm's-length boundary is the
thing that keeps the licenses separate.

Consequences:

- Do **not** vendor Piper's source or binary into a warble artifact you then
  redistribute (for example, do not publish a prebuilt Docker image that bundles
  `piper-tts`) without complying with GPL-3.0 for that image.
- The published `ghcr.io/rebelthor/warble-piper` image bundles unmodified
  `piper-tts` (the version pinned in `piper-shim/requirements.txt`, currently
  `1.4.2`). It is distributed under GPL-3.0: the image carries the label
  `org.opencontainers.image.licenses=GPL-3.0-or-later`, and the corresponding
  source is this repo's `piper-shim/Dockerfile` plus the matching upstream release
  of [`OHF-Voice/piper1-gpl`](https://github.com/OHF-Voice/piper1-gpl). The other
  three images (`warble-server`, `warble-whisper`, `warble-vad`) are MIT.
- `./warble start` can also build the Piper image locally (`./warble start --build`);
  building and running it for yourself carries no redistribution obligation.
- The Piper voice models are MIT and may be referenced/downloaded freely.
