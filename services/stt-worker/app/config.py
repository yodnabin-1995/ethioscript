"""STT Worker configuration."""

from pydantic import Field
from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    env: str = Field(default="development",            alias="ENV")
    metrics_port: int = Field(default=6004,                    alias="METRICS_PORT")

    rabbitmq_url: str = Field(alias="RABBITMQ_URL")
    rabbitmq_queue_stt: str = Field(default="ethioscript.jobs.stt", alias="RABBITMQ_QUEUE_STT")
    rabbitmq_queue_results: str = Field(default="ethioscript.jobs.results", alias="RABBITMQ_QUEUE_RESULTS")

    minio_endpoint: str = Field(alias="MINIO_ENDPOINT")
    minio_access_key: str = Field(alias="MINIO_ACCESS_KEY")
    minio_secret_key: str = Field(alias="MINIO_SECRET_KEY")
    minio_bucket: str = Field(default="ethioscript-assets",    alias="MINIO_BUCKET")
    minio_use_ssl: bool = Field(default=False,                   alias="MINIO_USE_SSL")

    job_orchestrator_url: str = Field(default="http://job-orchestrator:6002", alias="JOB_ORCHESTRATOR_URL")
    internal_api_key: str = Field(alias="INTERNAL_API_KEY")

    stt_model: str = Field(default="whisper-small",   alias="STT_MODEL")
    stt_language: str = Field(default="am",              alias="STT_LANGUAGE")
    stt_chunk_seconds: int = Field(default=30,                alias="STT_CHUNK_SECONDS")
    stt_worker_concurrency: int = Field(default=1,                 alias="STT_WORKER_CONCURRENCY")

    class Config:
        env_file = ".env"
        populate_by_name = True


settings = Settings()
