"""
STT Worker — Speech-to-Text pipeline (Whisper).

Consumes STT job messages from RabbitMQ, runs Whisper inference,
uploads transcript to MinIO, and publishes results back to the
job-orchestrator via the results queue.
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
from app.pipeline.stt_pipeline import run_stt

# ── Logging ────────────────────────────────────────────────────────────────────
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
    stream=sys.stdout,
)
logger = logging.getLogger("stt-worker")

# ── Prometheus metrics ─────────────────────────────────────────────────────────
JOBS_RECEIVED  = Counter("stt_jobs_received_total",  "Total STT jobs received")
JOBS_SUCCEEDED = Counter("stt_jobs_succeeded_total", "Total STT jobs succeeded")
JOBS_FAILED    = Counter("stt_jobs_failed_total",    "Total STT jobs failed", ["error_code"])
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
            job_id       = payload["job_id"]
            document_id  = payload["document_id"]
            tenant_id    = payload["tenant_id"]
            asset_key    = payload["asset_id"]      # MinIO object key
            language     = payload.get("language", settings.stt_language)

            logger.info("processing STT job %s (document: %s)", job_id, document_id)

            # 1. Download audio from MinIO
            audio_bytes = storage.download_object(asset_key)
            media_type = payload.get("media_type", "audio/mpeg")

            # 2. Run Whisper transcription pipeline
            result = run_stt(
                audio_bytes=audio_bytes,
                media_type=media_type,
                language=language,
                model_name=settings.stt_model,
                chunk_seconds=settings.stt_chunk_seconds,
            )

            if not result.success:
                raise RuntimeError(f"STT pipeline failed: {result.error_message}")

            # 3. Upload transcript JSON to MinIO
            transcript_key = storage.build_transcript_key(tenant_id, document_id, version=1)
            transcript_data = {
                "text": result.full_text,
                "segments": [
                    {"start": s.start, "end": s.end, "text": s.text}
                    for s in result.segments
                ],
                "language": result.language,
                "model": result.model_name,
                "duration_seconds": result.duration_seconds,
            }
            storage.upload_transcript(transcript_key, transcript_data)

            # 4. Build result message
            result_msg = {
                "job_id":            job_id,
                "document_id":       document_id,
                "tenant_id":         tenant_id,
                "success":           True,
                "raw_text_key":       transcript_key,
                "model_name":         result.model_name,
                "model_version":      result.model_version,
                "language":           result.language,
                "confidence_score":   0.0,    # Whisper doesn't provide confidence
                "page_count":         0,
                "word_count":         result.word_count,
                "char_count":         len(result.full_text),
                "processed_at":       datetime.now(timezone.utc).isoformat(),
            }

            duration = time.monotonic() - start
            PROCESSING_DURATION.observe(duration)
            JOBS_SUCCEEDED.inc()
            logger.info(
                "STT job %s completed: %d words, %.1f s audio in %.2f s",
                job_id, result.word_count, result.duration_seconds, duration,
            )
            return result_msg

        except Exception as exc:
            duration = time.monotonic() - start
            PROCESSING_DURATION.observe(duration)
            error_code = type(exc).__name__.upper()
            JOBS_FAILED.labels(error_code=error_code).inc()
            logger.exception("STT job failed: %s", exc)

            payload_data = json.loads(message.body) if message.body else {}
            return {
                "job_id":         payload_data.get("job_id", "unknown"),
                "document_id":    payload_data.get("document_id", "unknown"),
                "tenant_id":      payload_data.get("tenant_id", "unknown"),
                "success":         False,
                "error_code":      error_code,
                "error_message":   str(exc),
                "processed_at":   datetime.now(timezone.utc).isoformat(),
            }
        finally:
            ACTIVE_JOBS.dec()


async def main() -> None:
    logger.info(
        "starting STT worker | model=%s | language=%s",
        settings.stt_model,
        settings.stt_language,
    )

    # Start Prometheus metrics server in background thread
    Thread(target=start_http_server, args=(settings.metrics_port,), daemon=True).start()
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
    logger.info("STT worker ready")

    try:
        await asyncio.Future()
    finally:
        await connection.close()


if __name__ == "__main__":
    asyncio.run(main())
