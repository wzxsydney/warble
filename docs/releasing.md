# Releasing and images (maintainer)

warble ships four images on GHCR, all at one version that moves in lockstep:

- `ghcr.io/rebelthor/warble-server`
- `ghcr.io/rebelthor/warble-whisper`
- `ghcr.io/rebelthor/warble-piper`
- `ghcr.io/rebelthor/warble-vad`

`docker-compose.yml` pulls `:${WARBLE_VERSION:-latest}`. `./warble start` pulls these;
it builds locally only if a pull fails or you pass `./warble start --build`.

## Versioning

The git tag is the version. Tagging `vX.Y.Z` produces image tags `X.Y.Z`, `X.Y`,
`X`, `latest`, and `sha-<short>`. Pin `WARBLE_VERSION=X.Y.Z` in `.env` for a
reproducible setup; leave it `latest` to track releases.

Upstream component versions (e.g. whisper.cpp via the `WHISPER_TAG` build-arg in
`docker/whisper.Dockerfile`) are baked at build time and recorded in each image's
OCI labels. Bumping a component is a new warble release.

## Publishing the images

Two interchangeable ways (same Dockerfiles, same tags):

**Locally (no Actions minutes; good while the repo is private):**

```bash
scripts/publish-images.sh 1.2.0                  # multi-arch amd64 + arm64
PLATFORMS=linux/amd64 scripts/publish-images.sh 1.2.0   # single-arch, much faster
```

Multi-arch builds the non-native arch under QEMU (slow for the whisper compile).
If arm64 emulation isn't registered: `docker run --privileged tonistiigi/binfmt --install all`.

**Via GitHub Actions (free + automatic once the repo is public):** push a tag.

```bash
git tag v1.2.0 && git push origin v1.2.0
```

`.github/workflows/publish-images.yml` builds multi-arch and pushes on `v*` tags
(or run it manually with workflow_dispatch).

## GHCR package visibility

Images created from a private repo are private (a `docker login ghcr.io` is needed
to pull). When you make the project public, set each package's visibility to public
in GitHub → your packages → warble-* → Package settings, so adopters can pull
anonymously.

## Piper and GPL-3.0

The `warble-piper` image bundles `piper-tts` (OHF-Voice/piper1-gpl), which is
GPL-3.0. Publishing it is distributing GPL software, which is fine here because the
image is labeled `org.opencontainers.image.licenses=GPL-3.0-or-later` and the
corresponding source is available: this repo's `piper-shim/Dockerfile` plus the
unmodified upstream `piper-tts`. Keep that label in place (the publish script and
the workflow both set it). The other three images are MIT. See
[`THIRD_PARTY_LICENSES.md`](../THIRD_PARTY_LICENSES.md).
