"""
Silero VAD WebSocket sidecar.

One WebSocket = one utterance. Client streams 16 kHz mono int16 PCM as binary
frames; server streams back JSON {"event": "speech_start"|"speech_end", "ms": N}
events when transitions happen.

Query params (optional):
- threshold        speech probability cutoff (default 0.5)
- min_silence_ms   hangover after last speech before declaring end (default 600)
- speech_pad_ms    extra padding around detected speech (default 30)
- min_speech_ms    drop blips shorter than this (default 250)

Why these defaults: 600 ms hangover feels snappier than the ~700 ms used by
typical xiaozhi reference servers, without cutting off natural pauses in
conversational speech.

Silero v5 at 16 kHz requires exactly 512-sample chunks per inference. We buffer
incoming PCM and feed the iterator in 512-sample slices.
"""

import asyncio
import logging
import struct
import time

import numpy as np
from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from silero_vad import VADIterator, load_silero_vad

CHUNK_SAMPLES = 512  # Silero v5 at 16 kHz: fixed window
SAMPLE_RATE = 16000

logging.basicConfig(
    level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s"
)
log = logging.getLogger("vad-shim")

# Load the model once and share it across sessions (it's read-only — VADIterator
# wraps it with its own state).
MODEL = load_silero_vad(onnx=True)
log.info("silero VAD loaded (onnx)")

app = FastAPI()


@app.get("/health")
async def health() -> dict:
    return {"ok": True, "chunk_samples": CHUNK_SAMPLES, "sample_rate": SAMPLE_RATE}


def _qparam_float(ws: WebSocket, name: str, default: float) -> float:
    v = ws.query_params.get(name)
    if v is None:
        return default
    try:
        return float(v)
    except ValueError:
        return default


def _qparam_int(ws: WebSocket, name: str, default: int) -> int:
    v = ws.query_params.get(name)
    if v is None:
        return default
    try:
        return int(v)
    except ValueError:
        return default


@app.websocket("/vad")
async def vad_ws(ws: WebSocket) -> None:
    await ws.accept()
    threshold = _qparam_float(ws, "threshold", 0.5)
    min_silence_ms = _qparam_int(ws, "min_silence_ms", 600)
    speech_pad_ms = _qparam_int(ws, "speech_pad_ms", 30)
    min_speech_ms = _qparam_int(ws, "min_speech_ms", 250)

    vad = VADIterator(
        MODEL,
        threshold=threshold,
        sampling_rate=SAMPLE_RATE,
        min_silence_duration_ms=min_silence_ms,
        speech_pad_ms=speech_pad_ms,
    )
    # VADIterator doesn't expose min_speech_ms directly in older versions, but
    # the newer silero_vad.utils_vad version honors it via the `min_speech_duration_ms`
    # kwarg. Try to set it if available.
    try:
        vad.min_speech_samples = int(min_speech_ms * SAMPLE_RATE / 1000)
    except Exception:
        pass

    log.debug(
        "ws open client=%s threshold=%.2f silence=%dms pad=%dms min_speech=%dms",
        ws.client,
        threshold,
        min_silence_ms,
        speech_pad_ms,
        min_speech_ms,
    )

    pending = np.empty(0, dtype=np.float32)
    total_samples = 0
    started_at = time.monotonic()

    try:
        while True:
            msg = await ws.receive()
            if msg.get("type") == "websocket.disconnect":
                break
            data = msg.get("bytes")
            if data is None:
                # Text from client — accept "reset" to clear state mid-stream.
                text = msg.get("text") or ""
                if text == "reset":
                    vad.reset_states()
                    pending = np.empty(0, dtype=np.float32)
                    total_samples = 0
                continue

            # int16 little-endian → float32 in [-1, 1]
            if len(data) % 2 != 0:
                # truncate odd trailing byte (shouldn't happen with our wire format)
                data = data[:-1]
            ints = np.frombuffer(data, dtype="<i2")
            floats = ints.astype(np.float32) / 32768.0
            pending = np.concatenate([pending, floats]) if pending.size else floats

            while pending.size >= CHUNK_SAMPLES:
                chunk = pending[:CHUNK_SAMPLES]
                pending = pending[CHUNK_SAMPLES:]
                total_samples += CHUNK_SAMPLES

                result = vad(chunk, return_seconds=False)
                if not result:
                    continue
                if "start" in result:
                    ms = int(result["start"] * 1000 / SAMPLE_RATE)
                    await ws.send_json({"event": "speech_start", "ms": ms})
                if "end" in result:
                    ms = int(result["end"] * 1000 / SAMPLE_RATE)
                    elapsed = int((time.monotonic() - started_at) * 1000)
                    log.info(
                        "speech_end client=%s at=%dms wall=%dms",
                        ws.client,
                        ms,
                        elapsed,
                    )
                    await ws.send_json({"event": "speech_end", "ms": ms})

    except WebSocketDisconnect:
        pass
    except Exception:
        log.exception("ws error")
        try:
            await ws.close(code=1011)
        except Exception:
            pass
    finally:
        log.debug(
            "ws closed client=%s samples=%d wall=%dms",
            ws.client,
            total_samples,
            int((time.monotonic() - started_at) * 1000),
        )
