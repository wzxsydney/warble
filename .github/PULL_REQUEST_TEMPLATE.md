<!-- Thanks for contributing to warble. Keep PRs focused. -->

## What and why

Describe the change and the reason for it. Link any related issue.

## How it was tested

- Platform: Linux / macOS
- Install path: Docker (`./warble start`) / bare-metal (`bin/warble`)
- Device firmware tested against (if relevant):

## Checklist

- [ ] Change is focused; unrelated edits left out.
- [ ] No personal data, secrets, or machine-specific config committed (`.env`,
      live `prompt.txt`/`runtime.json` stay gitignored).
- [ ] New config knobs documented in the relevant `*.example` file and the README.
- [ ] Shell scripts pass `bash -n`; Go changes pass `gofmt`/`go vet`.
- [ ] Docs updated if behavior or commands changed.
