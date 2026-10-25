"""
OCR Worker — main entry point.

Consumes OCR job messages from RabbitMQ, runs the OCR pipeline,
uploads results to MinIO, and publishes a result message back to
the job-orchestrator via the results queue.
"""

import asyncio
import json
import logging
import sys
import time
import uuid
from datetime import datetime, timezone
from threading import Thread

import aio_pika
from prometheus_client import Counter, Gauge, Histogram, start_http_server

from app.adapters import storage
from app.config import settings
from app.pipeline.ocr_pipeline import run_ocr

# ── Logging — structured JSON ──────────────────────────────────────────────────

class _JsonFormatter(logging.Formatter):
    """Emit one valid JSON object per log record so docker logs can be parsed."""
    def format(self, record: logging.LogRecord) -> str:
        obj = {
            "time":  self.formatTime(record, "%Y-%m-%dT%H:%M:%S"),
            "level": record.levelname,
            "svc":   "ocr-worker",
            "msg":   record.getMessage(),
        }
        if record.exc_info:
            obj["exc"] = self.formatException(record.exc_info)
        return json.dumps(obj, ensure_ascii=False)

_handler = logging.StreamHandler(sys.stdout)
_handler.setFormatter(_JsonFormatter())
logging.root.setLevel(logging.INFO)
logging.root.handlers = [_handler]
logger = logging.getLogger("ocr-worker")

# ── Prometheus metrics ─────────────────────────────────────────────────────────
JOBS_RECEIVED = Counter("ocr_jobs_received_total", "Total OCR jobs received")
JOBS_SUCCEEDED = Counter("ocr_jobs_succeeded_total", "Total OCR jobs succeeded")
JOBS_FAILED = Counter("ocr_jobs_failed_total", "Total OCR jobs failed", ["error_code"])
PROCESSING_DURATION = Histogram(
    "ocr_processing_duration_seconds",
    "OCR job processing duration",
    buckets=[1, 5, 10, 30, 60, 120, 300],
)
ACTIVE_JOBS = Gauge("ocr_active_jobs", "Currently processing OCR jobs")


async def process_message(message: aio_pika.IncomingMessage) -> None:
    """Handle a single OCR job message."""
    async with message.process(requeue=False):
        JOBS_RECEIVED.inc()
        ACTIVE_JOBS.inc()
        start = time.monotonic()

        try:
            payload = json.loads(message.body)
            job_id = payload["job_id"]
            document_id = payload["document_id"]
            tenant_id = payload["tenant_id"]
            asset_object_key = payload["asset_id"]  # object key in MinIO
            language = payload.get("language", settings.ocr_language)

            logger.info("processing OCR job %s (document: %s)", job_id, document_id)

            # 1. Download source file
            file_bytes = storage.download_object(asset_object_key)
            media_type = payload.get("media_type", "application/pdf")

            # 2. Run OCR pipeline
            result = run_ocr(file_bytes, media_type, language)

            if not result.success:
                raise RuntimeError(f"OCR pipeline failed: {result.error_message}")

            # 3. Upload result text to MinIO
            result_key = storage.build_result_key(tenant_id, document_id, version=1)
            storage.upload_text(result_key, result.full_text)

            # 4. Build result message
            result_msg = {
                "job_id": job_id,
                "document_id": document_id,
                "tenant_id": tenant_id,
                "success": True,
                "raw_text_key": result_key,
                "model_name": result.model_name,
                "model_version": result.model_version,
                "language": result.language,
                "confidence_score": result.average_confidence,
                "page_count": result.page_count,
                "word_count": result.total_word_count,
                "char_count": result.total_char_count,
                "processed_at": datetime.now(timezone.utc).isoformat(),
            }

            duration = time.monotonic() - start
            PROCESSING_DURATION.observe(duration)
            JOBS_SUCCEEDED.inc()
            logger.info(
                "OCR job %s completed: %d pages, %d words in %.2fs",
                job_id, result.page_count, result.total_word_count, duration,
            )

            return result_msg

        except Exception as exc:
            duration = time.monotonic() - start
            PROCESSING_DURATION.observe(duration)
            error_code = type(exc).__name__.upper()
            JOBS_FAILED.labels(error_code=error_code).inc()
            logger.exception("OCR job failed: %s", exc)

            payload_data = json.loads(message.body) if message.body else {}
            return {
                "job_id": payload_data.get("job_id", "unknown"),
                "document_id": payload_data.get("document_id", "unknown"),
                "tenant_id": payload_data.get("tenant_id", "unknown"),
                "success": False,
                "error_code": error_code,
                "error_message": str(exc),
                "processed_at": datetime.now(timezone.utc).isoformat(),
            }
        finally:
            ACTIVE_JOBS.dec()


async def main() -> None:
    """Start the OCR worker."""
    logger.info(
        "starting OCR worker | concurrency=%d | language=%s",
        settings.ocr_worker_concurrency,
        settings.ocr_language,
    )

    # Start Prometheus metrics HTTP server in a background thread
    Thread(
        target=start_http_server,
        args=(settings.metrics_port,),
        daemon=True,
    ).start()
    logger.info("metrics server on port %d", settings.metrics_port)

    # Connect to RabbitMQ with retry
    for attempt in range(10):
        try:
            connection = await aio_pika.connect_robust(settings.rabbitmq_url)
            break
        except Exception as exc:
            wait = 2 ** attempt
            logger.warning("RabbitMQ not ready, retry in %ds: %s", wait, exc)
            await asyncio.sleep(wait)
    else:
        logger.error("could not connect to RabbitMQ after 10 attempts")
        sys.exit(1)

    channel = await connection.channel()
    await channel.set_qos(prefetch_count=settings.rabbitmq_prefetch)

    # Declare durable queues
    ocr_queue = await channel.declare_queue(
        settings.rabbitmq_queue_ocr, durable=True
    )
    results_exchange = await channel.declare_exchange(
        "ethioscript.events", aio_pika.ExchangeType.TOPIC, durable=True
    )

    logger.info("listening on queue: %s", settings.rabbitmq_queue_ocr)

    async def on_message(message: aio_pika.IncomingMessage) -> None:
        result_msg = await process_message(message)
        if result_msg:
            await results_exchange.publish(
                aio_pika.Message(
                    body=json.dumps(result_msg).encode(),
                    content_type="application/json",
                    delivery_mode=aio_pika.DeliveryMode.PERSISTENT,
                    message_id=str(uuid.uuid4()),
                ),
                routing_key="jobs.results",
            )

    await ocr_queue.consume(on_message)

    logger.info("OCR worker ready")
    try:
        await asyncio.Future()  # run forever
    finally:
        await connection.close()


if __name__ == "__main__":
    asyncio.run(main())
