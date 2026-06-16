# Security policy

warble is a hobby project, but security reports are welcome and taken seriously.

## Reporting a vulnerability

Please report privately, not in a public issue. Use GitHub's
[private vulnerability reporting](https://github.com/rebelthor/warble/security/advisories/new)
(Security tab, "Report a vulnerability"). Expect a best-effort response; this is a
spare-time project.

## Scope and known characteristics

warble is designed to run on a trusted home LAN. A few things to be aware of:

- **It listens on your LAN.** The server exposes a WebSocket (`:8000`) and an OTA
  endpoint (`:8003`) without authentication. Anyone on the same network can reach
  them. Run it on a network you trust; do not port-forward it to the internet.
- **The debug endpoints are off by default.** `/debug/sessions` and `/debug/say`
  are only mounted when the server runs with `WARBLE_DEBUG=1`. With them on, anyone
  on the LAN can make the robot speak. Leave debug off in normal use.
- **Serial logs contain Wi-Fi credentials.** When the device is configured over
  BLE, the firmware logs the Wi-Fi SSID and password in plaintext. Treat any
  captured serial logs (`bin/serial`, `idf-monitor`) as sensitive.
- **No audio or transcripts leave your machine.** That's the point of the project:
  speech-to-text, the model, and text-to-speech all run locally.

Issues in the upstream components warble orchestrates (whisper.cpp, Piper, Silero
VAD, Ollama, the `xiaozhi-esp32` firmware) should be reported to those projects.
