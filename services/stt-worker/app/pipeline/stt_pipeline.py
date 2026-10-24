"""
STT Pipeline — audio → transcript using OpenAI Whisper.

Steps:
  1. Write audio bytes to a temp file
  2. Validate / convert to WAV with pydub (handles MP3, MP4, OGG, WAV, WEBM)
  3. Split into chunks of `chunk_seconds` (avoids OOM on long audio)
  4. Transcribe each chunk with whisper.transcribe()
  5. Merge segment text + timestamps
  6. Return STTResult with full transcript, segments, word count, duration
"""

from __future__ import annotations

import io
import logging
import os
import tempfile
import time
from dataclasses import dataclass, field
from typing import Optional

logger = logging.getLogger(__name__)

# Lazy-load whisper and pydub so the container starts even if a wheel is missing
_whisper = None
_AudioSegment = None


def _get_whisper():
    global _whisper
    if _whisper is None:
        import whisper  # openai-whisper
        _whisper = whisper
    return _whisper


def _get_audio_segment():
    global _AudioSegment
    if _AudioSegment is None:
        from pydub import AudioSegment
        _AudioSegment = AudioSegment
    return _AudioSegment


# ─── Result types ─────────────────────────────────────────────────────────────

@dataclass
class Segment:
    start: float
    end: float
    text: str


@dataclass
class STTResult:
    success: bool
    full_text: str = ""
    segments: list[Segment] = field(default_factory=list)
    word_count: int = 0
    duration_seconds: float = 0.0
    model_name: str = "whisper"
    model_version: str = ""
    language: str = ""
    error_code: Optional[str] = None
    error_message: Optional[str] = None


# ─── Model cache — load once per worker process ──────────────────────────────

_model_cache: dict[str, any] = {}


def _load_model(model_name: str):
    if model_name not in _model_cache:
        whisper = _get_whisper()
        logger.info("loading Whisper model: %s", model_name)
        _model_cache[model_name] = whisper.load_model(model_name)
        logger.info("Whisper model loaded: %s", model_name)
    return _model_cache[model_name]


# ─── Public entry point ───────────────────────────────────────────────────────

def run_stt(
    audio_bytes: bytes,
    media_type: str,
    language: str,
    model_name: str = "whisper-small",
    chunk_seconds: int = 30,
) -> STTResult:
    """
    Transcribe audio_bytes using Whisper.

    Args:
        audio_bytes:   Raw audio file bytes.
        media_type:    MIME type (audio/mpeg, audio/wav, audio/mp4, audio/ogg, etc.)
        language:      ISO 639-1 language code, e.g. "am" for Amharic.
        model_name:    Whisper model size (tiny/base/small/medium/large).
        chunk_seconds: Split audio into this many seconds per chunk.

    Returns:
        STTResult with full transcript and per-segment data.
    """
    start = time.monotonic()

    # Map whisper model names — "whisper-small" → "small"
    wmodel = model_name.replace("whisper-", "").strip()

    try:
        model = _load_model(wmodel)
    except Exception as exc:
        return STTResult(
            success=False,
            error_code="MODEL_LOAD_FAILED",
            error_message=str(exc),
            model_name=model_name,
            language=language,
        )

    try:
        audio_segment = _load_audio(audio_bytes, media_type)
    except Exception as exc:
        logger.exception("audio load failed")
        return STTResult(
            success=False,
            error_code="AUDIO_LOAD_FAILED",
            error_message=str(exc),
            model_name=model_name,
            language=language,
        )

    total_duration = len(audio_segment) / 1000.0  # ms → seconds
    chunks = _split_audio(audio_segment, chunk_seconds)
    logger.info(
        "transcribing %.1f s audio in %d chunk(s) | model=%s language=%s",
        total_duration, len(chunks), wmodel, language,
    )

    all_segments: list[Segment] = []
    offset = 0.0

    for i, chunk in enumerate(chunks):
        try:
            chunk_segs = _transcribe_chunk(model, chunk, language, offset)
            all_segments.extend(chunk_segs)
        except Exception:
            logger.exception("chunk %d transcription failed", i)
            # Continue with remaining chunks rather than aborting
        offset += len(chunk) / 1000.0

    full_text = " ".join(s.text.strip() for s in all_segments if s.text.strip())
    word_count = len(full_text.split()) if full_text else 0
    elapsed = time.monotonic() - start

    logger.info(
        "STT completed: %.1f s audio → %d words in %.2f s",
        total_duration, word_count, elapsed,
    )

    return STTResult(
        success=True,
        full_text=full_text,
        segments=all_segments,
        word_count=word_count,
        duration_seconds=round(total_duration, 2),
        model_name=model_name,
        model_version=wmodel,
        language=language,
    )


# ─── Helpers ──────────────────────────────────────────────────────────────────

def _load_audio(audio_bytes: bytes, media_type: str):
    """Load audio bytes into a pydub AudioSegment, converting format as needed."""
    AudioSegment = _get_audio_segment()

    # Map MIME types to pydub format strings
    fmt_map = {
        "audio/mpeg":  "mp3",
        "audio/mp3":   "mp3",
        "audio/wav":   "wav",
        "audio/x-wav": "wav",
        "audio/mp4":   "mp4",
        "audio/m4a":   "mp4",
        "audio/ogg":   "ogg",
        "audio/webm":  "webm",
        "audio/flac":  "flac",
    }
    fmt = fmt_map.get(media_type.lower(), "mp3")

    audio = AudioSegment.from_file(io.BytesIO(audio_bytes), format=fmt)

    # Normalise to mono 16 kHz — Whisper works best with this
    audio = audio.set_channels(1).set_frame_rate(16000)
    return audio


def _split_audio(audio_segment, chunk_seconds: int) -> list:
    """Split a pydub AudioSegment into chunks of chunk_seconds each."""
    chunk_ms = chunk_seconds * 1000
    total_ms = len(audio_segment)
    if total_ms <= chunk_ms:
        return [audio_segment]
    return [
        audio_segment[start:start + chunk_ms]
        for start in range(0, total_ms, chunk_ms)
    ]


def _transcribe_chunk(model, chunk, language: str, time_offset: float) -> list[Segment]:
    """Write chunk to a temp WAV file and transcribe with Whisper."""
    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
        tmp_path = tmp.name
        chunk.export(tmp_path, format="wav")

    try:
        result = model.transcribe(
            tmp_path,
            language=language if language else None,
            task="transcribe",
            fp16=False,          # CPU-safe
            verbose=False,
        )
    finally:
        try:
            os.unlink(tmp_path)
        except OSError:
            pass

    segments = []
    for seg in result.get("segments", []):
        segments.append(Segment(
            start=round(seg["start"] + time_offset, 3),
            end=round(seg["end"] + time_offset, 3),
            text=seg["text"],
        ))

    # Fallback: if no segments but there is text, create a single segment
    if not segments and result.get("text", "").strip():
        segments.append(Segment(
            start=time_offset,
            end=time_offset,
            text=result["text"],
        ))

    return segments
