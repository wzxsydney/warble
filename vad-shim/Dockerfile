# Silero VAD WebSocket sidecar.
FROM python:3.12-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        curl && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY requirements.txt .
# Install CPU-only torch first. silero-vad declares torch as a dependency, but we
# run the model via onnxruntime (onnx=True), so the default multi-GB CUDA wheel is
# pure bloat. The CPU wheel (~200 MB) satisfies the dependency.
RUN pip install --no-cache-dir torch==2.12.0 --index-url https://download.pytorch.org/whl/cpu \
 && pip install --no-cache-dir -r requirements.txt
COPY vad_shim.py .
EXPOSE 8005
CMD ["uvicorn", "vad_shim:app", "--host", "0.0.0.0", "--port", "8005", "--log-level", "warning"]
