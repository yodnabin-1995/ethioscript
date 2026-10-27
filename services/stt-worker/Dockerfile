# ── STT Worker — Python + Whisper ────────────────────────────────────────────
FROM python:3.11-slim AS production

# Force apt to use HTTPS (port 443) — port 80 is blocked on this network
RUN echo 'Acquire::http::Pipeline-Depth "0";' > /etc/apt/apt.conf.d/99https && \
    sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources && \
    apt-get update && \
    apt-get install -y --no-install-recommends ffmpeg curl && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .

# Install in three steps:
# 1. Pin setuptools to a version that still ships pkg_resources (openai-whisper needs it)
# 2. Install torch/torchaudio CPU-only wheels (~250 MB vs ~2 GB for CUDA)
# 3. Install openai-whisper with --no-build-isolation (uses our pinned setuptools)
# 4. Install remaining deps, skipping torch/torchaudio so pip doesn't re-download
#    the full CUDA build from the default index.
RUN pip install --no-cache-dir pip==24.0 setuptools==69.5.1 wheel && \
    pip install --no-cache-dir \
        torch==2.2.2+cpu \
        torchaudio==2.2.2+cpu \
        --index-url https://download.pytorch.org/whl/cpu && \
    pip install --no-cache-dir --no-build-isolation openai-whisper==20231117 && \
    grep -vE "^torch(audio)?==|^openai-whisper==" requirements.txt > /tmp/requirements-no-torch.txt && \
    pip install --no-cache-dir -r /tmp/requirements-no-torch.txt

COPY app/ ./app/

# Pre-download Whisper model at build time so container has no internet dependency at runtime
RUN python -c "import whisper; whisper.load_model('small')"

RUN addgroup --gid 1001 worker && \
    adduser --uid 1001 --gid 1001 --disabled-password --gecos "" worker && \
    chown -R worker:worker /app

USER worker

EXPOSE 6004

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD curl -f http://localhost:6004/metrics || exit 1

CMD ["python", "-m", "app.main"]
