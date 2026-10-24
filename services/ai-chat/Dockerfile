# ── EthioScript AI Chat Service ───────────────────────────────────────────────
FROM python:3.11-slim

# Force apt to use HTTPS — port 80 is blocked on this network
RUN echo 'Acquire::http::Pipeline-Depth "0";' > /etc/apt/apt.conf.d/99https && \
    sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources && \
    apt-get update && apt-get install -y --no-install-recommends curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY main.py .

RUN groupadd --gid 1001 worker && \
    useradd --uid 1001 --gid 1001 --no-create-home --shell /bin/sh worker && \
    chown -R worker:worker /app

USER worker

EXPOSE 6006

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD curl -sf http://localhost:6006/health || exit 1

CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "6006"]
