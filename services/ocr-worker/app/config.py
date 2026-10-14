"""OCR Worker configuration loaded from environment variables."""

import os
from pydantic_settings import BaseSettings
from pydantic import Field


class Settings(BaseSettings):
    # Service
    env: str = Field(default="development", alias="ENV")
    metrics_port: int = Field(default=6003, alias="METRICS_PORT")

    # RabbitMQ
    rabbitmq_url: str = Field(alias="RABBITMQ_URL")
    rabbitmq_queue_ocr: str = Field(default="ethioscript.jobs.ocr", alias="RABBITMQ_QUEUE_OCR")
    rabbitmq_queue_results: str = Field(default="ethioscript.jobs.results", alias="RABBITMQ_QUEUE_RESULTS")
    rabbitmq_prefetch: int = Field(default=2, alias="RABBITMQ_PREFETCH")

    # MinIO / S3
    minio_endpoint: str = Field(alias="MINIO_ENDPOINT")
    minio_access_key: str = Field(alias="MINIO_ACCESS_KEY")
    minio_secret_key: str = Field(alias="MINIO_SECRET_KEY")
    minio_bucket: str = Field(default="ethioscript-assets", alias="MINIO_BUCKET")
    minio_use_ssl: bool = Field(default=False, alias="MINIO_USE_SSL")

    # Job Orchestrator callback
    job_orchestrator_url: str = Field(default="http://job-orchestrator:6002", alias="JOB_ORCHESTRATOR_URL")
    internal_api_key: str = Field(alias="INTERNAL_API_KEY")

    # OCR
    ocr_worker_concurrency: int = Field(default=2, alias="OCR_WORKER_CONCURRENCY")
    ocr_dpi: int = Field(default=300, alias="OCR_DPI")
    ocr_language: str = Field(default="amh+eng", alias="OCR_LANGUAGE")
    ocr_psm: int = Field(default=3, alias="OCR_PSM")
    ocr_oem: int = Field(default=3, alias="OCR_OEM")
    tessdata_prefix: str = Field(
        default="/usr/share/tesseract-ocr/5/tessdata",
        alias="TESSDATA_PREFIX",
    )
    ocr_deskew: bool = Field(default=True, alias="OCR_DESKEW")
    ocr_denoise: bool = Field(default=True, alias="OCR_DENOISE")
    ocr_upscale_threshold_dpi: int = Field(default=150, alias="OCR_UPSCALE_THRESHOLD_DPI")

    class Config:
        env_file = ".env"
        populate_by_name = True


settings = Settings()
