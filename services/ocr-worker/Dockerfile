# ── OCR Worker — Python 3.11 slim + Tesseract 5
# Uses python:3.11-slim which Docker may have cached from other Python services.
FROM python:3.11-slim AS production

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV DEBIAN_FRONTEND=noninteractive

RUN echo 'Acquire::http::Pipeline-Depth "0";' > /etc/apt/apt.conf.d/99https && \
    sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources && \
    apt-get update && apt-get install -y --no-install-recommends \
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

RUN tesseract --version && tesseract --list-langs

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir --upgrade pip setuptools wheel && \
    pip install --no-cache-dir -r requirements.txt

COPY app/ ./app/

RUN groupadd --gid 1001 worker && \
    useradd --uid 1001 --gid 1001 --no-create-home --shell /bin/sh worker && \
    chown -R worker:worker /app

USER worker

EXPOSE 6003

HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD curl -f http://localhost:6003/metrics || exit 1

CMD ["python", "-m", "app.main"]
