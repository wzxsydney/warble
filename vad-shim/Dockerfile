# Silero VAD WebSocket sidecar.
FROM python:3.12-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        curl && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY vad_shim.py .
EXPOSE 8005
CMD ["uvicorn", "vad_shim:app", "--host", "0.0.0.0", "--port", "8005", "--log-level", "warning"]
