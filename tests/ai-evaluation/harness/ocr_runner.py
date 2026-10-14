"""
ocr_runner.py — Runs OCR on a test fixture using Tesseract directly
(for offline evaluation) or via the live OCR worker (for integration eval).

Two modes:
  LOCAL  — calls pytesseract directly on the local machine
  LIVE   — uploads to MinIO, triggers job via document-service, polls for result
"""

import logging
import os
import time
from enum import Enum
from pathlib import Path
from typing import Optional

logger = logging.getLogger(__name__)


class RunnerMode(Enum):
    LOCAL = "local"
    LIVE  = "live"


# ─── Local runner (Tesseract direct) ─────────────────────────────────────────

def run_local_ocr(
    image_path: Path,
    language: str = "amh+eng",
    dpi: int = 300,
    psm: int = 3,
    oem: int = 3,
) -> tuple[str, float]:
    """
    Run Tesseract directly on the image and return (extracted_text, duration_seconds).

    Requires Tesseract 5 with Amharic tessdata installed:
      apt install tesseract-ocr tesseract-ocr-amh tesseract-ocr-eng
    """
    try:
        import pytesseract
        from PIL import Image
    except ImportError as exc:
        raise RuntimeError(f"pytesseract/Pillow not available: {exc}")

    tessdata = os.environ.get("TESSDATA_PREFIX", "/usr/share/tesseract-ocr/5/tessdata")
    os.environ["TESSDATA_PREFIX"] = tessdata

    config = f"--oem {oem} --psm {psm}"

    start = time.monotonic()

    if image_path.suffix.lower() == ".pdf":
        from pdf2image import convert_from_path
        pages = convert_from_path(str(image_path), dpi=dpi)
        texts = []
        for page in pages:
            texts.append(pytesseract.image_to_string(page, lang=language, config=config))
        text = "\n\n".join(texts)
    else:
        image = Image.open(image_path)
        text = pytesseract.image_to_string(image, lang=language, config=config)

    duration = time.monotonic() - start
    return text.strip(), duration


# ─── Live runner (via running services) ──────────────────────────────────────

def run_live_ocr(
    image_path: Path,
    language: str,
    account_url: str,
    document_url: str,
    access_token: str,
    poll_interval: int = 3,
    max_wait: int = 120,
) -> tuple[str, float, dict]:
    """
    Run OCR via the live microservice stack:
    1. Request presigned upload URL from account-service (asset endpoint)
    2. PUT file directly to MinIO
    3. POST /documents to document-service
    4. Poll job-orchestrator until REVIEW_REQUIRED or COMPLETED
    5. GET /documents/{id}/result for extracted text

    Returns (extracted_text, duration_seconds, model_metadata).
    """
    import requests

    headers = {
        "Authorization": f"Bearer {access_token}",
        "Content-Type": "application/json",
    }

    start = time.monotonic()

    # Step 1: Initiate upload
    media_type = _media_type(image_path)
    size_bytes = image_path.stat().st_size

    upload_resp = requests.post(
        f"{account_url}/assets",
        headers=headers,
        json={
            "filename":         image_path.name,
            "media_type":       media_type,
            "size_bytes":       size_bytes,
            "checksum_sha256":  _sha256(image_path),
        },
        timeout=15,
    )
    upload_resp.raise_for_status()
    upload_data = upload_resp.json()
    asset_id   = upload_data["asset_id"]
    upload_url = upload_data["upload_url"]

    # Step 2: PUT file to presigned URL
    with open(image_path, "rb") as f:
        put_resp = requests.put(
            upload_url,
            data=f,
            headers={"Content-Type": media_type},
            timeout=60,
        )
    if put_resp.status_code not in (200, 204):
        raise RuntimeError(f"Upload PUT failed: {put_resp.status_code}")

    # Step 3: Create document
    doc_resp = requests.post(
        f"{document_url}/documents",
        headers=headers,
        json={
            "asset_id":  asset_id,
            "filename":  image_path.name,
            "language":  language,
            "task_type": "OCR",
        },
        timeout=15,
    )
    doc_resp.raise_for_status()
    doc_data = doc_resp.json()
    doc_id   = doc_data["id"]

    logger.info("created document %s — polling for result", doc_id)

    # Step 4: Poll for completion
    terminal_states = {"REVIEW_REQUIRED", "APPROVED", "FAILED", "CANCELLED"}
    deadline = time.monotonic() + max_wait

    while time.monotonic() < deadline:
        status_resp = requests.get(
            f"{document_url}/documents/{doc_id}",
            headers=headers,
            timeout=10,
        )
        status_resp.raise_for_status()
        status_data = status_resp.json()
        state = status_data.get("status", "")

        logger.debug("document %s status: %s", doc_id, state)
        if state in terminal_states:
            break
        time.sleep(poll_interval)
    else:
        raise TimeoutError(f"Document {doc_id} did not reach terminal state in {max_wait}s")

    if status_data.get("status") == "FAILED":
        raise RuntimeError(f"OCR job failed for document {doc_id}")

    # Step 5: Get result
    result_resp = requests.get(
        f"{document_url}/documents/{doc_id}/result",
        headers=headers,
        timeout=10,
    )
    result_resp.raise_for_status()
    result_data = result_resp.json()

    duration = time.monotonic() - start
    text     = result_data.get("approved_text") or result_data.get("raw_text_inline") or ""
    metadata = result_data.get("model_metadata") or {}

    return text, duration, metadata


# ─── Helpers ──────────────────────────────────────────────────────────────────

def _media_type(path: Path) -> str:
    ext = path.suffix.lower()
    return {
        ".pdf":  "application/pdf",
        ".png":  "image/png",
        ".jpg":  "image/jpeg",
        ".jpeg": "image/jpeg",
        ".tiff": "image/tiff",
        ".webp": "image/webp",
    }.get(ext, "application/octet-stream")


def _sha256(path: Path) -> str:
    import hashlib
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()
