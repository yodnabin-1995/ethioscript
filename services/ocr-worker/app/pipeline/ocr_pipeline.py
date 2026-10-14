"""
OCR Pipeline — image/PDF → extracted text.

Pre-processing steps:
  1. Convert PDF pages to images (pdf2image)
  2. Deskew if enabled (OpenCV)
  3. Denoise if enabled (OpenCV)
  4. Upscale if DPI is below threshold
  5. Run Tesseract OCR
"""

import io
import logging
import math
import os
import time
from dataclasses import dataclass, field
from typing import Optional

import cv2
import numpy as np
import pytesseract
from pdf2image import convert_from_bytes
from PIL import Image

from app.config import settings

logger = logging.getLogger(__name__)


@dataclass
class PageResult:
    page_number: int
    text: str
    confidence: float
    word_count: int
    char_count: int


@dataclass
class OCRResult:
    success: bool
    pages: list[PageResult] = field(default_factory=list)
    full_text: str = ""
    average_confidence: float = 0.0
    total_word_count: int = 0
    total_char_count: int = 0
    page_count: int = 0
    model_name: str = "tesseract"
    model_version: str = ""
    language: str = ""
    error_code: Optional[str] = None
    error_message: Optional[str] = None
    duration_seconds: float = 0.0


def get_tesseract_version() -> str:
    """Returns the installed Tesseract version string."""
    try:
        return pytesseract.get_tesseract_version().vstring
    except Exception:
        return "unknown"


def run_ocr(file_bytes: bytes, media_type: str, language: str = None) -> OCRResult:
    """
    Run the full OCR pipeline on file_bytes.

    Args:
        file_bytes: Raw bytes of the file.
        media_type:  MIME type string, e.g. "application/pdf" or "image/jpeg".
        language:    Tesseract language code(s), e.g. "amh+eng". Falls back to settings.

    Returns:
        OCRResult with per-page results and aggregate statistics.
    """
    start = time.monotonic()
    lang = language or settings.ocr_language
    tess_version = get_tesseract_version()

    try:
        images = _load_images(file_bytes, media_type)
    except Exception as exc:
        logger.exception("image loading failed")
        return OCRResult(
            success=False,
            error_code="IMAGE_LOAD_FAILED",
            error_message=str(exc),
            model_name="tesseract",
            model_version=tess_version,
            language=lang,
        )

    pages: list[PageResult] = []
    for i, image in enumerate(images, start=1):
        try:
            page_result = _process_page(image, i, lang)
            pages.append(page_result)
        except Exception as exc:
            logger.exception("page %d processing failed", i)
            pages.append(PageResult(
                page_number=i,
                text="",
                confidence=0.0,
                word_count=0,
                char_count=0,
            ))

    full_text = "\n\n".join(p.text for p in pages if p.text)
    total_words = sum(p.word_count for p in pages)
    total_chars = sum(p.char_count for p in pages)
    avg_conf = (
        sum(p.confidence for p in pages) / len(pages)
        if pages else 0.0
    )

    duration = time.monotonic() - start
    logger.info(
        "OCR completed: %d pages, %d words, avg_conf=%.2f, duration=%.2fs",
        len(pages), total_words, avg_conf, duration,
    )

    return OCRResult(
        success=True,
        pages=pages,
        full_text=full_text,
        average_confidence=round(avg_conf, 4),
        total_word_count=total_words,
        total_char_count=total_chars,
        page_count=len(pages),
        model_name="tesseract",
        model_version=tess_version,
        language=lang,
        duration_seconds=round(duration, 3),
    )


def _load_images(file_bytes: bytes, media_type: str) -> list[Image.Image]:
    """Convert file bytes to a list of PIL Images."""
    if media_type == "application/pdf":
        return convert_from_bytes(
            file_bytes,
            dpi=settings.ocr_dpi,
            fmt="png",
        )
    else:
        return [Image.open(io.BytesIO(file_bytes))]


def _process_page(image: Image.Image, page_number: int, language: str) -> PageResult:
    """Pre-process a single image and run Tesseract."""
    np_img = np.array(image.convert("RGB"))
    np_img = cv2.cvtColor(np_img, cv2.COLOR_RGB2BGR)

    # Convert to grayscale
    gray = cv2.cvtColor(np_img, cv2.COLOR_BGR2GRAY)

    # Deskew
    if settings.ocr_deskew:
        gray = _deskew(gray)

    # Denoise
    if settings.ocr_denoise:
        gray = cv2.fastNlMeansDenoising(gray, h=10)

    # Upscale if DPI is too low (heuristic: small image)
    height, width = gray.shape[:2]
    if width < 1000 or height < 1000:
        scale = 2.0
        gray = cv2.resize(gray, None, fx=scale, fy=scale, interpolation=cv2.INTER_CUBIC)

    # Binarize (adaptive threshold)
    binary = cv2.adaptiveThreshold(
        gray, 255,
        cv2.ADAPTIVE_THRESH_GAUSSIAN_C,
        cv2.THRESH_BINARY, 11, 2,
    )

    # Run Tesseract with data output for confidence
    tess_config = f"--oem {settings.ocr_oem} --psm {settings.ocr_psm}"
    if settings.tessdata_prefix:
        os.environ["TESSDATA_PREFIX"] = settings.tessdata_prefix

    pil_binary = Image.fromarray(binary)
    data = pytesseract.image_to_data(
        pil_binary,
        lang=language,
        config=tess_config,
        output_type=pytesseract.Output.DICT,
    )

    # Extract text and mean confidence
    words = []
    confidences = []
    for j, conf in enumerate(data["conf"]):
        if isinstance(conf, (int, float)) and conf > 0:
            word = data["text"][j].strip()
            if word:
                words.append(word)
                confidences.append(float(conf))

    text = pytesseract.image_to_string(pil_binary, lang=language, config=tess_config)
    avg_conf = sum(confidences) / len(confidences) if confidences else 0.0

    return PageResult(
        page_number=page_number,
        text=text.strip(),
        confidence=round(avg_conf / 100.0, 4),  # normalise to 0-1
        word_count=len(words),
        char_count=len(text.strip()),
    )


def _deskew(image: np.ndarray) -> np.ndarray:
    """Correct skew using the projection method."""
    coords = np.column_stack(np.where(image < 128))
    if len(coords) == 0:
        return image
    angle = cv2.minAreaRect(coords.astype(np.float32))[-1]
    if angle < -45:
        angle = -(90 + angle)
    else:
        angle = -angle
    if abs(angle) < 0.5:
        return image  # skip tiny corrections
    (h, w) = image.shape[:2]
    center = (w // 2, h // 2)
    M = cv2.getRotationMatrix2D(center, angle, 1.0)
    return cv2.warpAffine(
        image, M, (w, h),
        flags=cv2.INTER_CUBIC,
        borderMode=cv2.BORDER_REPLICATE,
    )
