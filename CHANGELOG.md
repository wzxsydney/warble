# Changelog

Notable changes to warble. Versions correspond to git tags (`vX.Y.Z`) and the
matching `ghcr.io/rebelthor/warble-*` image tags. See [docs/releasing.md](docs/releasing.md).

## Unreleased

- Initial public-ready state: single `./warble` command (start/stop/status/logs/
  transcript/restart/connect/disconnect), Docker Compose with prebuilt GHCR images
  and a local-build fallback, healthcheck-gated startup, neutral English defaults,
  and the bare-metal `bin/warble` supervisor.
