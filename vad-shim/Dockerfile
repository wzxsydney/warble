# Silero VAD WebSocket sidecar.
FROM python:3.12-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        curl && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY requirements.txt .
# Install torch AND torchaudio together from the CPU index. silero-vad imports
# torchaudio at load time; if torchaudio comes from PyPI it's built against a
# different torch ABI and its native lib (_torchaudio.abi3.so) fails to load on
# arm64 (OSError). Both must come from the CPU index so the ABIs match. Pin torch
# explicitly: torch==2.12.0+cpu has no CUDA deps, but if torch is left for pip to
# resolve via torchaudio it picks a CUDA build and drags in a ~540 MB nvidia wheel.
# torch and torchaudio version numbers have diverged, so they are pinned separately.
RUN pip install --no-cache-dir torch==2.12.0 torchaudio==2.11.0 --index-url https://download.pytorch.org/whl/cpu \
 && pip install --no-cache-dir -r requirements.txt
COPY vad_shim.py .
EXPOSE 8005
CMD ["uvicorn", "vad_shim:app", "--host", "0.0.0.0", "--port", "8005", "--log-level", "warning"]
