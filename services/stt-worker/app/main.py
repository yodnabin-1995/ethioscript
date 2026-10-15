"""
STT Worker — Speech-to-Text pipeline (Release 2).

Consumes STT job messages from RabbitMQ, runs Whisper inference,
uploads transcript to MinIO, and publishes results back.

STATUS: Release 2 stub — message consumption and result publishing
        are wired; Whisper model loading is conditional on availability.
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

from app.config import settings

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
    stream=sys.stdout,
)
logger = logging.getLogger("stt-worker")

# ── Prometheus metrics ────────────────────────────────────────────────────────
JOBS_RECEIVED = Counter("stt_jobs_received_total",  "Total STT jobs received")
JOBS_SUCCEEDED = Counter("stt_jobs_succeeded_total", "Total STT jobs succeeded")
JOBS_FAILED = Counter("stt_jobs_failed_total",    "Total STT jobs failed", ["error_code"])
PROCESSING_DURATION = Histogram(
    "stt_processing_duration_seconds",
    "STT job processing duration",
    buckets=[5, 15, 30, 60, 120, 300, 600],
)
ACTIVE_JOBS = Gauge("stt_active_jobs", "Currently processing STT jobs")


async def process_message(message: aio_pika.IncomingMessage) -> dict:
    """Handle a single STT job message."""
    async with message.process(requeue=False):
        JOBS_RECEIVED.inc()
        ACTIVE_JOBS.inc()
        start = time.monotonic()

        try:
            payload = json.loads(message.body)
            job_id = payload["job_id"]

            logger.info("processing STT job %s", job_id)

            # TODO (Release 2): implement full STT pipeline
            # 1. Download audio from MinIO
            # 2. Validate and normalise audio format (pydub / ffmpeg)
            # 3. Split into chunks of settings.stt_chunk_seconds
            # 4. Run whisper.transcribe() on each chunk
            # 5. Merge segments with timestamps
            # 6. Upload transcript JSON to MinIO
            # 7. Return result message

            raise NotImplementedError("STT pipeline not yet implemented (Release 2)")

        except NotImplementedError as exc:
            error_code = "NOT_IMPLEMENTED"
            JOBS_FAILED.labels(error_code=error_code).inc()
            logger.warning("STT job %s: %s", payload.get("job_id", "?"), exc)
            return {
                "job_id":       payload.get("job_id", "unknown"),
                "document_id":  payload.get("document_id", "unknown"),
                "tenant_id":    payload.get("tenant_id", "unknown"),
                "success":      False,
                "error_code":   error_code,
                "error_message": str(exc),
                "processed_at": datetime.now(timezone.utc).isoformat(),
            }
        except Exception as exc:
            error_code = type(exc).__name__.upper()
            JOBS_FAILED.labels(error_code=error_code).inc()
            logger.exception("STT job failed")
            return {
                "job_id":       payload.get("job_id", "unknown"),
                "document_id":  payload.get("document_id", "unknown"),
                "tenant_id":    payload.get("tenant_id", "unknown"),
                "success":      False,
                "error_code":   error_code,
                "error_message": str(exc),
                "processed_at": datetime.now(timezone.utc).isoformat(),
            }
        finally:
            ACTIVE_JOBS.dec()
            PROCESSING_DURATION.observe(time.monotonic() - start)


async def main() -> None:
    logger.info(
        "starting STT worker | model=%s | language=%s",
        settings.stt_model,
        settings.stt_language,
    )

    Thread(target=start_http_server, args=(settings.metrics_port,), daemon=True).start()
    logger.info("metrics server on port %d", settings.metrics_port)

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
    await channel.set_qos(prefetch_count=settings.stt_worker_concurrency)

    stt_queue = await channel.declare_queue(settings.rabbitmq_queue_stt, durable=True)
    results_exchange = await channel.declare_exchange(
        "ethioscript.events", aio_pika.ExchangeType.TOPIC, durable=True
    )

    logger.info("listening on queue: %s", settings.rabbitmq_queue_stt)

    async def on_message(message: aio_pika.IncomingMessage) -> None:
        result = await process_message(message)
        if result:
            await results_exchange.publish(
                aio_pika.Message(
                    body=json.dumps(result).encode(),
                    content_type="application/json",
                    delivery_mode=aio_pika.DeliveryMode.PERSISTENT,
                    message_id=str(uuid.uuid4()),
                ),
                routing_key="jobs.results",
            )

    await stt_queue.consume(on_message)
    logger.info("STT worker ready (Release 2 stub)")

    try:
        await asyncio.Future()
    finally:
        await connection.close()


if __name__ == "__main__":
    asyncio.run(main())
