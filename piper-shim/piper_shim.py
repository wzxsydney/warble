"""
FastAPI shim exposing OpenAI-compatible /v1/audio/speech against Piper voices.

Pre-loads every .onnx in the same directory at startup. Picks the voice for a
request via the `voice` body field (e.g. "en_US-lessac-medium"). Falls back to
DEFAULT_VOICE (env PIPER_DEFAULT_VOICE) if the field is missing or unknown.
"""

import io
import os
import re
import wave
from glob import glob

from fastapi import FastAPI
from fastapi.responses import Response
from piper import PiperVoice

# Strip code points whose names eSpeak would synthesize literally
# (emoji, symbol pictographs, CJK, etc.) before TTS.
_STRIP_RE = re.compile(
    "["
    "\U0001F300-\U0001FAFF"   # symbols & pictographs, emoticons, transport, supplemental
    "\U00002600-\U000027BF"   # misc symbols + dingbats
    "\U0001F1E6-\U0001F1FF"   # regional indicator (flags)
    "一-鿿"           # CJK unified ideographs
    "㐀-䶿"           # CJK extension A
    "　-〿"           # CJK symbols & punctuation
    "＀-￯"           # halfwidth + fullwidth forms
    "぀-ゟ"           # Hiragana
    "゠-ヿ"           # Katakana
    "가-힯"           # Hangul syllables
    "‍️"             # ZWJ + variation selector-16
    "]+",
    flags=re.UNICODE,
)


def _clean(text: str) -> str:
    return _STRIP_RE.sub("", text).strip()

DEFAULT_VOICE = os.environ.get("PIPER_DEFAULT_VOICE", "en_US-lessac-medium")
HERE = os.path.dirname(os.path.abspath(__file__))
# Where the *.onnx voices live. Defaults to ../models/piper (bare-metal); the
# Docker image sets PIPER_VOICES_DIR=/voices and mounts the host models there.
VOICES_DIR = os.environ.get(
    "PIPER_VOICES_DIR", os.path.join(HERE, os.pardir, "models", "piper")
)

voices: dict[str, PiperVoice] = {}
for onnx in glob(os.path.join(VOICES_DIR, "*.onnx")):
    stem = os.path.basename(onnx)[: -len(".onnx")]
    print(f"loading {stem}...")
    voices[stem] = PiperVoice.load(onnx)
print(f"voices loaded: {sorted(voices)}; default: {DEFAULT_VOICE}")

app = FastAPI()


def _silent_wav(rate: int = 22050) -> bytes:
    """Tiny silent WAV so upstream doesn't see an HTTP error on empty input."""
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(b"\x00\x00" * (rate // 20))  # 50 ms silence
    return buf.getvalue()


@app.post("/v1/audio/speech")
async def speak(body: dict):
    text = _clean(body.get("input", ""))
    if not text:
        return Response(_silent_wav(), media_type="audio/wav")

    voice_name = body.get("voice") or DEFAULT_VOICE
    voice = voices.get(voice_name) or voices.get(DEFAULT_VOICE)
    if voice is None:
        return Response("no voices loaded", status_code=500)

    buf = io.BytesIO()
    try:
        with wave.open(buf, "wb") as w:
            voice.synthesize_wav(text, w)
    except Exception:
        return Response(_silent_wav(), media_type="audio/wav")
    return Response(buf.getvalue(), media_type="audio/wav")
