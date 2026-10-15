# ── OCR Worker — Python + Tesseract + Amharic tessdata ────────────────────────
FROM python:3.12-slim AS production

# System deps: Tesseract 5 + Amharic + English tessdata, poppler (pdf2image),
# libgl (OpenCV headless)
# NOTE: tesseract-ocr-orm (Oromo) is NOT in Debian repos — download manually if needed
RUN apt-get update && apt-get install -y --no-install-recommends \
    tesseract-ocr \
    tesseract-ocr-amh \
    tesseract-ocr-eng \
    tesseract-ocr-tir \
    poppler-utils \
    libglib2.0-0 \
    libsm6 \
    libxext6 \
    libxrender-dev \
    libgomp1 \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Verify tesseract
RUN tesseract --version && tesseract --list-langs

WORKDIR /app

# Pre-install setuptools to avoid pkg_resources issues with legacy packages
COPY requirements.txt .
RUN pip install --no-cache-dir --upgrade pip setuptools wheel && \
    pip install --no-cache-dir -r requirements.txt

# Copy application code
COPY app/ ./app/

# Non-root user for security
RUN addgroup --gid 1001 worker && \
    adduser --uid 1001 --gid 1001 --disabled-password --gecos "" worker && \
    chown -R worker:worker /app

USER worker

# Expose Prometheus metrics port
EXPOSE 6003

# Health check via metrics endpoint
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD curl -f http://localhost:6003/metrics || exit 1

CMD ["python", "-m", "app.main"]
