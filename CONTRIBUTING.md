# Contributing to warble

Thanks for your interest. warble is a hobby project; expect informal, best-effort
maintenance.

## Ways to help

- Report setup problems (use the "Setup help" issue template). Hardware + host
  details matter; vague reports are hard to act on.
- Report bugs with logs (`docker compose logs` or `log/*.log`).
- Add support for another language/voice/model and document it.
- Improve the docs, the Docker setup, or the bare-metal path on a platform you run.

## Ground rules

- Keep warble's own code MIT-compatible. Do not vendor GPL source/binaries
  (notably Piper) into the repo; integrate over HTTP / as an installed dependency.
  See [`THIRD_PARTY_LICENSES.md`](THIRD_PARTY_LICENSES.md).
- Keep personalization out of the repo. Language, voice, model, and persona are
  user config (`*.example` files); the committed defaults stay neutral English.
- Match the surrounding style. Go: `gofmt`. Keep changes focused.

## Development

- Go server: `cd server && go build ./... && go vet ./...`. Wire-level tests via
  `cmd/testclient` (see [`server/README.md`](server/README.md)).
- Protocol reference: [`server/PROTOCOL.md`](server/PROTOCOL.md).
- Bare-metal ops: [`docs/operations.md`](docs/operations.md).

## Pull requests

Describe what changed and how you tested it (which host, Docker or bare metal,
which device firmware). Note any new config knobs and add them to the relevant
`*.example` file and the README.
