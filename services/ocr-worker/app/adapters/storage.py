"""MinIO / S3-compatible storage adapter for the OCR worker."""

import io
import logging
from typing import Optional

from minio import Minio
from minio.error import S3Error

from app.config import settings

logger = logging.getLogger(__name__)

_client: Optional[Minio] = None


def get_client() -> Minio:
    global _client
    if _client is None:
        _client = Minio(
            settings.minio_endpoint,
            access_key=settings.minio_access_key,
            secret_key=settings.minio_secret_key,
            secure=settings.minio_use_ssl,
        )
    return _client


def download_object(object_key: str) -> bytes:
    """Download an object from MinIO and return its bytes."""
    client = get_client()
    try:
        response = client.get_object(settings.minio_bucket, object_key)
        data = response.read()
        response.close()
        response.release_conn()
        logger.debug("downloaded object: %s (%d bytes)", object_key, len(data))
        return data
    except S3Error as exc:
        logger.error("S3 download error: %s — %s", object_key, exc)
        raise


def upload_text(object_key: str, text: str) -> str:
    """Upload extracted text to MinIO and return the object key."""
    client = get_client()
    encoded = text.encode("utf-8")
    try:
        client.put_object(
            settings.minio_bucket,
            object_key,
            io.BytesIO(encoded),
            length=len(encoded),
            content_type="text/plain; charset=utf-8",
        )
        logger.debug("uploaded text artifact: %s", object_key)
        return object_key
    except S3Error as exc:
        logger.error("S3 upload error: %s — %s", object_key, exc)
        raise


def build_result_key(tenant_id: str, document_id: str, version: int) -> str:
    """Build a deterministic object key for the OCR result text."""
    return f"{tenant_id}/results/{document_id}/v{version}/raw_text.txt"
