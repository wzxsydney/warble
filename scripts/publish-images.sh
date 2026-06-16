#!/usr/bin/env bash
# Build and push the four warble images to GHCR (maintainer tool).
# Same images and tags the GitHub Actions workflow produces, but from your machine,
# which avoids Actions minutes (relevant while the repo is private).
#
#   scripts/publish-images.sh 1.2.0                  # multi-arch (amd64 + arm64) + latest
#   PLATFORMS=linux/amd64 scripts/publish-images.sh 1.2.0   # single-arch (much faster)
#   GHCR_OWNER=youruser scripts/publish-images.sh 1.2.0
#
# Multi-arch builds the non-native arch under QEMU emulation (slow for the whisper
# compile). If arm64 emulation isn't set up: docker run --privileged tonistiigi/binfmt --install all
set -euo pipefail

VERSION="${1:?usage: publish-images.sh <version>  e.g. 1.2.0}"
OWNER="${GHCR_OWNER:-rebelthor}"
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; cd "$ROOT"
SOURCE="https://github.com/$OWNER/warble"

# Log in to GHCR with the gh token if available.
if command -v gh >/dev/null 2>&1; then
    gh auth token | docker login ghcr.io -u "$OWNER" --password-stdin >/dev/null
fi

# A buildx builder that supports multi-platform.
docker buildx inspect warble-builder >/dev/null 2>&1 || docker buildx create --name warble-builder >/dev/null
docker buildx use warble-builder

# build NAME CONTEXT DOCKERFILE LICENSE
build() {
    local img="ghcr.io/$OWNER/warble-$1"
    echo "==> $img:$VERSION  ($PLATFORMS)"
    docker buildx build \
        --platform "$PLATFORMS" \
        -f "$3" \
        -t "$img:$VERSION" -t "$img:latest" \
        --label "org.opencontainers.image.version=$VERSION" \
        --label "org.opencontainers.image.source=$SOURCE" \
        --label "org.opencontainers.image.licenses=$4" \
        --push \
        "$2"
}

build server  server     server/Dockerfile         MIT
build whisper docker      docker/whisper.Dockerfile MIT
build vad     vad-shim    vad-shim/Dockerfile       MIT
# Piper bundles piper-tts (GPL-3.0). Corresponding source: this Dockerfile +
# upstream github.com/OHF-Voice/piper1-gpl. The image is labeled accordingly.
build piper   piper-shim  piper-shim/Dockerfile     GPL-3.0-or-later

echo "done: pushed warble-{server,whisper,vad,piper}:$VERSION (+ latest) to ghcr.io/$OWNER"
