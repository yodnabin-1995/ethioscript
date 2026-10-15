# ── STT Worker — Python + Whisper (Release 2) ─────────────────────────────────
FROM python:3.12-slim AS production

RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
# Pre-install setuptools to avoid pkg_resources missing error with openai-whisper
RUN pip install --no-cache-dir --upgrade pip setuptools wheel && \
    pip install --no-cache-dir -r requirements.txt

COPY app/ ./app/

RUN addgroup --gid 1001 worker && \
    adduser --uid 1001 --gid 1001 --disabled-password --gecos "" worker && \
    chown -R worker:worker /app

USER worker

EXPOSE 6004

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD curl -f http://localhost:6004/metrics || exit 1

CMD ["python", "-m", "app.main"]
