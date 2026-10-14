# ── OCR Worker — Python + Tesseract + Amharic tessdata ────────────────────────
FROM python:3.12-slim AS production

# System deps: Tesseract 5 + Amharic + English tessdata, poppler (pdf2image),
# libgl (OpenCV headless)
RUN apt-get update && apt-get install -y --no-install-recommends \
    tesseract-ocr \
    tesseract-ocr-amh \
    tesseract-ocr-eng \
    tesseract-ocr-tir \
    tesseract-ocr-orm \
    poppler-utils \
    libglib2.0-0 \
    libsm6 \
    libxext6 \
    libxrender-dev \
    libgomp1 \
    wget \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Verify tesseract installation
RUN tesseract --version && tesseract --list-langs

WORKDIR /app

# Install Python dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir --upgrade pip && \
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
