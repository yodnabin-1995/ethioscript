"""
conftest.py — pytest session configuration for AI evaluation.

Ensures reports directory exists and logs evaluation configuration
at session start.
"""

import logging
import os
from pathlib import Path

import pytest

REPORTS_DIR = Path(__file__).parent / "reports"
logger = logging.getLogger(__name__)


def pytest_configure(config):
    """Create reports directory before any tests run."""
    REPORTS_DIR.mkdir(parents=True, exist_ok=True)


def pytest_sessionstart(session):
    logger.info("=" * 60)
    logger.info("EthioScript AI Evaluation Session")
    logger.info("  OCR_LANGUAGE    : %s", os.environ.get("OCR_LANGUAGE", "amh+eng"))
    logger.info("  OCR_CER_THRESHOLD: %s", os.environ.get("OCR_CER_THRESHOLD", "0.15"))
    logger.info("  OCR_MODEL        : %s v%s",
                os.environ.get("OCR_MODEL_NAME", "tesseract"),
                os.environ.get("OCR_MODEL_VERSION", "5.x"))
    logger.info("  USE_SYNTHETIC    : %s", os.environ.get("OCR_USE_SYNTHETIC", "true"))
    logger.info("  Reports dir      : %s", REPORTS_DIR)
    logger.info("=" * 60)


def pytest_sessionfinish(session, exitstatus):
    summary = REPORTS_DIR / "latest_summary.txt"
    if summary.exists():
        logger.info("\n%s", summary.read_text(encoding="utf-8"))
    logger.info("Evaluation complete. Exit status: %d", exitstatus)
