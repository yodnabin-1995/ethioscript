"""
metrics.py — CER and WER computation for EthioScript AI evaluation.

Follows SRS Section 7.3:
  CER = (substitutions + deletions + insertions) / reference characters
  WER = (substitutions + deletions + insertions) / reference words

Normalization rules (SRS Section 7.3):
  - Unicode NFC normalization
  - Ethiopic punctuation and whitespace normalisation
  - Configurable: strip punctuation, lowercase, collapse whitespace
"""

import re
import unicodedata
from dataclasses import dataclass, field
from typing import Optional

from jiwer import cer as jiwer_cer
from jiwer import wer as jiwer_wer
from jiwer import process_characters, process_words


# ─── Ethiopic-aware normalisation ────────────────────────────────────────────

# Ethiopic word separators (Ethiopic word space ፡ and full stop ።)
ETHIOPIC_PUNCTUATION = re.compile(r"[፡።፣፤፥፦፧፨]")
# Multiple whitespace → single space
MULTI_SPACE = re.compile(r"\s+")


def normalize_ethiopic(text: str, *, strip_punct: bool = True) -> str:
    """
    Normalise Ethiopian-script text for fair CER/WER comparison.

    Steps:
    1. Unicode NFC normalisation (canonical composition)
    2. Strip Ethiopic punctuation marks (optional)
    3. Collapse multiple whitespace characters to a single space
    4. Strip leading/trailing whitespace
    """
    if not text:
        return ""
    # NFC: canonical decomposition, then canonical composition
    text = unicodedata.normalize("NFC", text)
    if strip_punct:
        text = ETHIOPIC_PUNCTUATION.sub(" ", text)
        # Also strip common ASCII punctuation that may appear in mixed documents
        text = re.sub(r"[.,;:!?()\[\]{}\"""''`~@#$%^&*\-_+=|\\/<>]", " ", text)
    text = MULTI_SPACE.sub(" ", text).strip()
    return text


# ─── Result dataclasses ───────────────────────────────────────────────────────

@dataclass
class PageMetrics:
    page_number: int
    reference: str
    hypothesis: str
    cer: float
    wer: float
    ref_chars: int
    hyp_chars: int
    ref_words: int
    hyp_words: int
    char_substitutions: int
    char_deletions: int
    char_insertions: int
    word_substitutions: int
    word_deletions: int
    word_insertions: int


@dataclass
class DocumentMetrics:
    document_id: str
    language: str
    document_class: str          # e.g. "printed", "handwritten", "degraded"
    model_name: str
    model_version: str
    page_metrics: list[PageMetrics] = field(default_factory=list)
    # Aggregated across all pages
    aggregate_cer: float = 0.0
    aggregate_wer: float = 0.0
    page_count: int = 0
    failed_pages: int = 0
    total_ref_chars: int = 0
    total_ref_words: int = 0
    processing_seconds: float = 0.0


@dataclass
class EvaluationReport:
    """Top-level report across all evaluated documents."""
    run_id: str
    timestamp: str
    model_name: str
    model_version: str
    language: str
    document_class: str
    total_documents: int
    passed_documents: int          # met CER threshold
    failed_documents: int          # did not meet threshold
    cer_threshold: float           # target from SRS acceptance criteria
    overall_cer: float
    overall_wer: float
    per_class_cer: dict[str, float] = field(default_factory=dict)
    per_class_wer: dict[str, float] = field(default_factory=dict)
    document_results: list[DocumentMetrics] = field(default_factory=list)
    notes: list[str] = field(default_factory=list)


# ─── Core metric functions ────────────────────────────────────────────────────

def compute_cer(reference: str, hypothesis: str) -> tuple[float, dict]:
    """
    Compute Character Error Rate between reference and hypothesis.

    Returns:
        (cer_score, detail_dict) where detail_dict contains substitutions,
        deletions, insertions, and reference character count.
    """
    if not reference:
        return 0.0, {"substitutions": 0, "deletions": 0, "insertions": 0, "ref_chars": 0}

    ref_norm = normalize_ethiopic(reference)
    hyp_norm = normalize_ethiopic(hypothesis) if hypothesis else ""

    if not ref_norm:
        return 0.0, {"substitutions": 0, "deletions": 0, "insertions": 0, "ref_chars": 0}

    try:
        result = process_characters(ref_norm, hyp_norm)
        cer_score = jiwer_cer(ref_norm, hyp_norm)
        return cer_score, {
            "substitutions": result.substitutions,
            "deletions":     result.deletions,
            "insertions":    result.insertions,
            "ref_chars":     len(ref_norm.replace(" ", "")),
        }
    except Exception as exc:
        return 1.0, {
            "error": str(exc),
            "substitutions": 0, "deletions": 0, "insertions": 0,
            "ref_chars": len(ref_norm.replace(" ", "")),
        }


def compute_wer(reference: str, hypothesis: str) -> tuple[float, dict]:
    """
    Compute Word Error Rate between reference and hypothesis.

    Returns:
        (wer_score, detail_dict) with substitutions, deletions, insertions,
        and reference word count.
    """
    if not reference:
        return 0.0, {"substitutions": 0, "deletions": 0, "insertions": 0, "ref_words": 0}

    ref_norm = normalize_ethiopic(reference)
    hyp_norm = normalize_ethiopic(hypothesis) if hypothesis else ""

    if not ref_norm:
        return 0.0, {"substitutions": 0, "deletions": 0, "insertions": 0, "ref_words": 0}

    try:
        result = process_words(ref_norm, hyp_norm)
        wer_score = jiwer_wer(ref_norm, hyp_norm)
        return wer_score, {
            "substitutions": result.substitutions,
            "deletions":     result.deletions,
            "insertions":    result.insertions,
            "ref_words":     len(ref_norm.split()),
        }
    except Exception as exc:
        return 1.0, {
            "error": str(exc),
            "substitutions": 0, "deletions": 0, "insertions": 0,
            "ref_words": len(ref_norm.split()),
        }


def evaluate_page(
    page_number: int,
    reference: str,
    hypothesis: str,
) -> PageMetrics:
    """Evaluate a single page and return full PageMetrics."""
    cer_score, cer_detail = compute_cer(reference, hypothesis)
    wer_score, wer_detail = compute_wer(reference, hypothesis)

    ref_norm = normalize_ethiopic(reference)
    hyp_norm = normalize_ethiopic(hypothesis) if hypothesis else ""

    return PageMetrics(
        page_number        = page_number,
        reference          = reference,
        hypothesis         = hypothesis,
        cer                = round(cer_score, 6),
        wer                = round(wer_score, 6),
        ref_chars          = len(ref_norm.replace(" ", "")),
        hyp_chars          = len(hyp_norm.replace(" ", "")),
        ref_words          = len(ref_norm.split()),
        hyp_words          = len(hyp_norm.split()),
        char_substitutions = cer_detail.get("substitutions", 0),
        char_deletions     = cer_detail.get("deletions", 0),
        char_insertions    = cer_detail.get("insertions", 0),
        word_substitutions = wer_detail.get("substitutions", 0),
        word_deletions     = wer_detail.get("deletions", 0),
        word_insertions    = wer_detail.get("insertions", 0),
    )


def aggregate_document_metrics(doc: DocumentMetrics) -> DocumentMetrics:
    """
    Compute aggregate CER/WER across all pages of a document using the
    'corpus-level' method: sum all errors / sum all reference chars (words).
    This is more reliable than averaging per-page scores.
    """
    total_char_sub = total_char_del = total_char_ins = 0
    total_word_sub = total_word_del = total_word_ins = 0
    total_ref_chars = total_ref_words = 0

    for pm in doc.page_metrics:
        total_char_sub  += pm.char_substitutions
        total_char_del  += pm.char_deletions
        total_char_ins  += pm.char_insertions
        total_word_sub  += pm.word_substitutions
        total_word_del  += pm.word_deletions
        total_word_ins  += pm.word_insertions
        total_ref_chars += pm.ref_chars
        total_ref_words += pm.ref_words

    doc.total_ref_chars = total_ref_chars
    doc.total_ref_words = total_ref_words
    doc.page_count = len(doc.page_metrics)

    doc.aggregate_cer = round(
        (total_char_sub + total_char_del + total_char_ins) / max(total_ref_chars, 1), 6
    )
    doc.aggregate_wer = round(
        (total_word_sub + total_word_del + total_word_ins) / max(total_ref_words, 1), 6
    )
    return doc
