# Piper TTS shim. piper-tts (OHF-Voice/piper1-gpl) is GPL-3.0 and is installed
# from upstream at build time (pinned in requirements.txt), so this image contains
# GPL software. The published warble-piper image is distributed under GPL-3.0:
# labeled org.opencontainers.image.licenses=GPL-3.0-or-later, with corresponding
# source being this Dockerfile + upstream piper1-gpl. See ../THIRD_PARTY_LICENSES.md.
FROM python:3.12-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        espeak-ng curl && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY piper_shim.py .
# Voices are mounted at /voices (see docker-compose.yml).
ENV PIPER_VOICES_DIR=/voices
EXPOSE 8001
CMD ["uvicorn", "piper_shim:app", "--host", "0.0.0.0", "--port", "8001"]
