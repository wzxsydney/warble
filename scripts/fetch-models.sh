#!/usr/bin/env bash
# Download the models warble needs into ./models, for both the bare-metal and
# Docker paths. Idempotent: skips files that already exist.
#
#   scripts/fetch-models.sh                  # defaults: whisper "base", voice en_US-lessac-medium
#   WHISPER_MODEL_NAME=large-v3 scripts/fetch-models.sh
#   PIPER_VOICE=de_DE-thorsten-medium scripts/fetch-models.sh
#
# Sizes (approx): whisper base ~150 MB, small ~500 MB, medium ~1.5 GB, large-v3 ~3 GB.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WHISPER_DIR="$ROOT/models/whisper"
PIPER_DIR="$ROOT/models/piper"
mkdir -p "$PIPER_DIR"

WHISPER_MODEL_NAME="${WHISPER_MODEL_NAME:-base}"
PIPER_VOICE="${PIPER_VOICE:-en_US-lessac-medium}"

mkdir -p "$WHISPER_DIR"

# --- whisper.cpp ggml model -------------------------------------------------
WHISPER_FILE="$WHISPER_DIR/ggml-${WHISPER_MODEL_NAME}.bin"
if [ -f "$WHISPER_FILE" ]; then
    echo "whisper model present: $WHISPER_FILE"
else
    echo "downloading whisper ggml model '$WHISPER_MODEL_NAME' (this can be large)..."
    curl -fL --progress-bar \
        "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-${WHISPER_MODEL_NAME}.bin" \
        -o "$WHISPER_FILE"
    echo "  -> $WHISPER_FILE"
fi

# --- Piper voice (.onnx + .onnx.json) --------------------------------------
# Voice id format: <lang>_<REGION>-<name>-<quality>, e.g. en_US-lessac-medium.
voice_path() {
    # en_US-lessac-medium -> en/en_US/lessac/medium/en_US-lessac-medium
    local v="$1"
    local lang="${v%%_*}"
    local rest="${v#*-}"          # lessac-medium
    local region="${v%%-*}"       # en_US
    local name="${rest%-*}"       # lessac
    local quality="${rest##*-}"   # medium
    echo "${lang}/${region}/${name}/${quality}/${v}"
}
VP="$(voice_path "$PIPER_VOICE")"
BASE="https://huggingface.co/rhasspy/piper-voices/resolve/main/${VP}"
for ext in onnx onnx.json; do
    OUT="$PIPER_DIR/${PIPER_VOICE}.${ext}"
    if [ -f "$OUT" ]; then
        echo "piper voice file present: $OUT"
    else
        echo "downloading piper voice file: ${PIPER_VOICE}.${ext}"
        curl -fL --progress-bar "${BASE}.${ext}" -o "$OUT"
        echo "  -> $OUT"
    fi
done

echo "done. whisper: $WHISPER_FILE ; piper voice: $PIPER_DIR/${PIPER_VOICE}.onnx"
