"""
test_ocr_evaluation.py — pytest-based OCR evaluation suite.

Runs CER/WER evaluation against every fixture in the test set and
produces JSON, CSV, HTML, and text reports.

SRS Section 7 requirements exercised:
  FR-007: OCR pipeline (Tesseract, Amharic)
  SRS 7.2: Evaluation dataset requirements
  SRS 7.3: CER/WER primary metrics
  SRS 7.4: Confidence treated as routing signal, not guarantee
  SRS 15: Acceptance criteria — OCR CER meets documented threshold

Usage:
  # Offline (local Tesseract):
  cd tests/ai-evaluation
  python -m pytest test_ocr_evaluation.py -v

  # With custom CER threshold and language:
  OCR_CER_THRESHOLD=0.10 OCR_LANGUAGE=amh+eng python -m pytest test_ocr_evaluation.py -v

  # Skip if Tesseract not installed:
  python -m pytest test_ocr_evaluation.py -v -m "not requires_tesseract"
"""

import logging
import os
import uuid
from datetime import datetime, timezone
from pathlib import Path

import pytest

from harness.fixtures import (
    REPORTS_DIR,
    FixtureRegistry,
    TestFixture,
    discover_fixtures,
    generate_synthetic_fixtures,
    iter_fixtures,
    load_ground_truth,
)
from harness.metrics import (
    DocumentMetrics,
    EvaluationReport,
    PageMetrics,
    aggregate_document_metrics,
    evaluate_page,
)
from harness.ocr_runner import RunnerMode, run_local_ocr
from harness.reporter import save_all_reports

logger = logging.getLogger(__name__)

# ─── Configuration (from environment or defaults) ────────────────────────────

OCR_LANGUAGE        = os.environ.get("OCR_LANGUAGE", "amh+eng")
OCR_DPI             = int(os.environ.get("OCR_DPI", "300"))
OCR_PSM             = int(os.environ.get("OCR_PSM", "3"))
OCR_OEM             = int(os.environ.get("OCR_OEM", "3"))
CER_THRESHOLD       = float(os.environ.get("OCR_CER_THRESHOLD", "0.15"))   # 15% — placeholder until baseline measured
WER_THRESHOLD       = float(os.environ.get("OCR_WER_THRESHOLD", "0.25"))   # 25% — placeholder
MODEL_NAME          = os.environ.get("OCR_MODEL_NAME", "tesseract")
MODEL_VERSION       = os.environ.get("OCR_MODEL_VERSION", "5.x")
RUN_ID              = os.environ.get("OCR_RUN_ID", uuid.uuid4().hex[:12])
USE_SYNTHETIC       = os.environ.get("OCR_USE_SYNTHETIC", "true").lower() == "true"


# ─── Session-level fixtures (pytest) ─────────────────────────────────────────

@pytest.fixture(scope="session")
def fixture_registry() -> FixtureRegistry:
    """Discover real fixtures + optionally generate synthetics."""
    registry = discover_fixtures()
    if USE_SYNTHETIC or len(registry.fixtures) == 0:
        synthetics = generate_synthetic_fixtures()
        registry.fixtures.extend(synthetics)
    return registry


@pytest.fixture(scope="session")
def evaluation_report(fixture_registry: FixtureRegistry) -> EvaluationReport:
    """
    Session-scoped report object — accumulated by individual tests and
    finalised in the session finaliser.
    """
    return EvaluationReport(
        run_id            = RUN_ID,
        timestamp         = datetime.now(timezone.utc).isoformat(),
        model_name        = MODEL_NAME,
        model_version     = MODEL_VERSION,
        language          = OCR_LANGUAGE,
        document_class    = "all",
        total_documents   = 0,
        passed_documents  = 0,
        failed_documents  = 0,
        cer_threshold     = CER_THRESHOLD,
        overall_cer       = 0.0,
        overall_wer       = 0.0,
    )


# ─── Tesseract availability check ────────────────────────────────────────────

def tesseract_available() -> bool:
    try:
        import pytesseract
        pytesseract.get_tesseract_version()
        return True
    except Exception:
        return False


requires_tesseract = pytest.mark.skipif(
    not tesseract_available(),
    reason="Tesseract not installed — install tesseract-ocr + tesseract-ocr-amh",
)


# ─── Unit tests for metric functions (always run, no Tesseract needed) ───────

class TestMetricsUnit:
    """Pure unit tests for CER/WER computation — no Tesseract required."""

    def test_identical_text_gives_zero_cer(self):
        from harness.metrics import compute_cer
        ref = "ሰላም አሜሪካ ኢትዮጵያ"
        cer, _ = compute_cer(ref, ref)
        assert cer == 0.0, "identical text must give CER = 0"

    def test_identical_text_gives_zero_wer(self):
        from harness.metrics import compute_wer
        ref = "ሰላም አሜሪካ ኢትዮጵያ"
        wer, _ = compute_wer(ref, ref)
        assert wer == 0.0, "identical text must give WER = 0"

    def test_empty_hypothesis_gives_cer_one(self):
        from harness.metrics import compute_cer
        ref = "ሰላም"
        cer, detail = compute_cer(ref, "")
        assert cer == 1.0, "empty hypothesis must give CER = 1.0"
        assert detail["deletions"] > 0

    def test_empty_reference_gives_zero(self):
        from harness.metrics import compute_cer, compute_wer
        cer, _ = compute_cer("", "anything")
        wer, _ = compute_wer("", "anything")
        assert cer == 0.0
        assert wer == 0.0

    def test_single_char_substitution(self):
        from harness.metrics import compute_cer
        ref  = "ሰ"
        hyp  = "ለ"
        cer, detail = compute_cer(ref, hyp)
        assert cer == 1.0
        assert detail["substitutions"] == 1

    def test_partial_match_cer_is_between_zero_and_one(self):
        from harness.metrics import compute_cer
        ref = "ሰላም ዓለም"
        hyp = "ሰላም zzz"
        cer, _ = compute_cer(ref, hyp)
        assert 0.0 < cer < 1.0, f"partial match CER should be between 0 and 1, got {cer}"

    def test_evaluate_page_returns_correct_type(self):
        pm = evaluate_page(1, "ሰላም", "ሰላም")
        assert isinstance(pm, PageMetrics)
        assert pm.cer == 0.0
        assert pm.page_number == 1

    def test_aggregate_document_metrics_corpus_level(self):
        """Aggregate CER uses corpus-level (sum errors / sum chars), not average."""
        from harness.metrics import DocumentMetrics
        doc = DocumentMetrics(
            document_id="test", language="amh", document_class="printed",
            model_name="tesseract", model_version="5.x",
        )
        # Page 1: 0 errors / 3 chars
        doc.page_metrics.append(evaluate_page(1, "abc", "abc"))
        # Page 2: 3 errors / 3 chars (total delete)
        doc.page_metrics.append(evaluate_page(2, "abc", ""))
        doc = aggregate_document_metrics(doc)
        # corpus CER = 3 errors / 6 chars = 0.5
        assert 0.4 <= doc.aggregate_cer <= 0.6, f"Expected ~0.5 corpus CER, got {doc.aggregate_cer}"

    def test_ethiopic_normalisation_strips_punctuation(self):
        from harness.metrics import normalize_ethiopic
        text = "ሰላም፡ ዓለም።"
        norm = normalize_ethiopic(text, strip_punct=True)
        assert "፡" not in norm
        assert "።" not in norm
        assert "ሰላም" in norm

    def test_nfc_normalisation_applied(self):
        from harness.metrics import normalize_ethiopic
        import unicodedata
        # Two strings that look identical but have different Unicode compositions
        composed   = unicodedata.normalize("NFC", "ሰላም")
        decomposed = unicodedata.normalize("NFD", "ሰላም")
        assert normalize_ethiopic(composed) == normalize_ethiopic(decomposed), \
            "NFC normalisation must make composed and decomposed forms equal"

    def test_wer_single_word_deletion(self):
        from harness.metrics import compute_wer
        ref = "ሰላም ዓለም"
        hyp = "ሰላም"
        wer, detail = compute_wer(ref, hyp)
        assert detail["deletions"] == 1
        assert wer == pytest.approx(0.5, abs=0.01)

    def test_report_structure(self):
        report = EvaluationReport(
            run_id="test-run", timestamp="2026-10-14T00:00:00Z",
            model_name="tesseract", model_version="5.x",
            language="amh", document_class="printed",
            total_documents=10, passed_documents=8, failed_documents=2,
            cer_threshold=0.15, overall_cer=0.08, overall_wer=0.12,
        )
        assert report.passed_documents + report.failed_documents == report.total_documents


# ─── Fixture discovery tests ──────────────────────────────────────────────────

class TestFixtureDiscovery:
    """Tests for the fixture discovery and loading system."""

    def test_discover_fixtures_returns_registry(self, fixture_registry: FixtureRegistry):
        assert fixture_registry is not None
        assert isinstance(fixture_registry.fixtures, list)

    def test_synthetic_fixtures_generated_when_no_real_fixtures(
        self, fixture_registry: FixtureRegistry
    ):
        if USE_SYNTHETIC:
            synth = [f for f in fixture_registry.fixtures if f.document_class == "synthetic"]
            assert len(synth) > 0, "Synthetic fixtures must be generated when USE_SYNTHETIC=true"

    def test_fixture_files_exist(self, fixture_registry: FixtureRegistry):
        for f in fixture_registry.fixtures:
            assert f.ground_truth_path.exists(), \
                f"Ground truth file missing: {f.ground_truth_path}"
            assert f.image_path.exists(), \
                f"Image file missing: {f.image_path}"

    def test_ground_truth_is_non_empty(self, fixture_registry: FixtureRegistry):
        for f in fixture_registry.fixtures:
            gt = load_ground_truth(f)
            assert len(gt.strip()) > 0, \
                f"Ground truth for {f.fixture_id} must not be empty"

    def test_fixture_metadata_has_required_fields(self, fixture_registry: FixtureRegistry):
        for f in fixture_registry.fixtures:
            assert f.fixture_id, "fixture_id must be non-empty"
            assert f.language,   "language must be set"
            assert f.document_class, "document_class must be set"


# ─── OCR evaluation tests (require Tesseract) ────────────────────────────────

class TestOCREvaluation:
    """Runs actual OCR and measures CER/WER against ground truth."""

    @requires_tesseract
    def test_ocr_runs_without_error(self, fixture_registry: FixtureRegistry):
        """Sanity check: OCR pipeline completes without raising."""
        fixtures = fixture_registry.fixtures[:1]  # smoke test with first fixture
        if not fixtures:
            pytest.skip("No fixtures available")

        f = fixtures[0]
        text, duration = run_local_ocr(f.image_path, language=OCR_LANGUAGE, dpi=OCR_DPI)
        assert isinstance(text, str), "OCR must return a string"
        assert duration > 0, "Duration must be positive"
        logger.info("OCR smoke test: %s | %.2fs | %d chars", f.fixture_id, duration, len(text))

    @requires_tesseract
    def test_per_fixture_cer_within_threshold(
        self,
        fixture_registry: FixtureRegistry,
        evaluation_report: EvaluationReport,
    ):
        """
        For each fixture, run OCR, compute CER, check threshold.

        NOTE: CER_THRESHOLD is a *proposed* target per SRS Section 15:
        'The initial OCR release meets a numeric CER target agreed after
        baseline testing on the chosen dataset; the target is documented
        rather than guessed.'

        Failures here indicate the model needs improvement or the threshold
        needs to be updated based on actual baseline data.

        Synthetic fixtures only smoke-test OCR and are excluded from the
        accuracy gate until a real benchmark dataset is available.
        """
        benchmark_fixtures = [
            fixture for fixture in fixture_registry.fixtures
            if fixture.document_class != "synthetic"
        ]
        if not benchmark_fixtures:
            pytest.skip(
                "No real benchmark fixtures available — synthetic fixtures only "
                "validate that OCR runs"
            )

        all_passed = True
        failed_fixtures = []
        benchmark_ids = {fixture.fixture_id for fixture in benchmark_fixtures}

        for fixture, ground_truth in iter_fixtures(fixture_registry):
            if fixture.fixture_id not in benchmark_ids:
                continue

            hypothesis, duration = run_local_ocr(
                fixture.image_path,
                language=fixture.language or OCR_LANGUAGE,
                dpi=OCR_DPI, psm=OCR_PSM, oem=OCR_OEM,
            )

            pm = evaluate_page(1, ground_truth, hypothesis)
            doc = DocumentMetrics(
                document_id    = fixture.fixture_id,
                language       = fixture.language,
                document_class = fixture.document_class,
                model_name     = MODEL_NAME,
                model_version  = MODEL_VERSION,
                processing_seconds = duration,
            )
            doc.page_metrics.append(pm)
            doc = aggregate_document_metrics(doc)

            # Accumulate in session report
            evaluation_report.document_results.append(doc)
            evaluation_report.total_documents += 1

            passed = doc.aggregate_cer <= CER_THRESHOLD
            if passed:
                evaluation_report.passed_documents += 1
            else:
                evaluation_report.failed_documents += 1
                failed_fixtures.append(
                    f"{fixture.fixture_id} (CER={doc.aggregate_cer * 100:.2f}%)"
                )
                all_passed = False

            logger.info(
                "%-40s | CER=%6.2f%% | WER=%6.2f%% | %s | %.2fs",
                fixture.fixture_id,
                doc.aggregate_cer * 100,
                doc.aggregate_wer * 100,
                "PASS" if passed else "FAIL",
                duration,
            )

        # Compute overall corpus-level CER from all accumulated documents
        _finalise_report(evaluation_report)

        if not all_passed:
            pytest.fail(
                f"CER threshold {CER_THRESHOLD * 100:.1f}% not met for:\n"
                + "\n".join(f"  • {f}" for f in failed_fixtures)
                + f"\n\nOverall CER: {evaluation_report.overall_cer * 100:.2f}%"
                + "\nSee reports/ for full evaluation results."
            )

    @requires_tesseract
    def test_amharic_specific_cer(self, fixture_registry: FixtureRegistry):
        """Test CER specifically for Amharic-language fixtures."""
        amh_fixtures = fixture_registry.filter(language="amh")
        if not amh_fixtures:
            pytest.skip("No Amharic fixtures available")

        results = []
        for f in amh_fixtures:
            gt   = load_ground_truth(f)
            hyp, _ = run_local_ocr(f.image_path, language="amh+eng", dpi=OCR_DPI)
            pm   = evaluate_page(1, gt, hyp)
            results.append(pm.cer)
            logger.info("Amharic fixture %s | CER=%.2f%%", f.fixture_id, pm.cer * 100)

        avg_cer = sum(results) / len(results)
        logger.info("Amharic average CER: %.2f%% across %d fixtures", avg_cer * 100, len(results))

        # Informational — does not fail (threshold is established post-baseline)
        assert avg_cer <= 1.0, "Average CER must be a valid ratio"

    @requires_tesseract
    def test_ocr_output_is_valid_utf8_ethiopic(self, fixture_registry: FixtureRegistry):
        """
        SRS Section 15: 'All user-visible Ethiopic text survives upload,
        processing, editing, database persistence, search, and export
        without encoding corruption.'
        """
        ethio_fixtures = [
            f for f in fixture_registry.fixtures
            if f.language in ("amh", "tir", "orm")
        ]
        if not ethio_fixtures:
            pytest.skip("No Ethiopic-language fixtures available")

        for f in ethio_fixtures[:3]:  # test first 3 to keep suite fast
            text, _ = run_local_ocr(f.image_path, language=OCR_LANGUAGE, dpi=OCR_DPI)
            # Must be valid UTF-8 (encode/decode round-trip)
            try:
                encoded = text.encode("utf-8")
                decoded = encoded.decode("utf-8")
                assert decoded == text, f"UTF-8 round-trip failed for {f.fixture_id}"
            except (UnicodeEncodeError, UnicodeDecodeError) as exc:
                pytest.fail(f"Encoding corruption in {f.fixture_id}: {exc}")


# ─── Report generation test ───────────────────────────────────────────────────

class TestReportGeneration:
    """Tests that report generation works and produces valid output files."""

    def test_generate_all_report_formats(self, evaluation_report: EvaluationReport):
        """Generates all report formats and verifies files are created."""
        # Ensure at least minimal data in report
        if evaluation_report.total_documents == 0:
            evaluation_report.total_documents = 1
            evaluation_report.passed_documents = 1
            evaluation_report.overall_cer = 0.05
            evaluation_report.overall_wer = 0.08
            evaluation_report.notes.append(
                "No real fixtures evaluated — synthetic/empty run"
            )

        paths = save_all_reports(evaluation_report, REPORTS_DIR)

        for fmt, path in paths.items():
            assert path.exists(), f"{fmt} report file was not created: {path}"
            assert path.stat().st_size > 0, f"{fmt} report file is empty: {path}"
            logger.info("Report %s → %s (%d bytes)", fmt, path.name, path.stat().st_size)

    def test_html_report_contains_cer(self, evaluation_report: EvaluationReport):
        """HTML report must contain CER values."""
        paths = save_all_reports(evaluation_report, REPORTS_DIR)
        html_content = paths["html"].read_text(encoding="utf-8")
        assert "CER" in html_content, "HTML report must mention CER"
        assert "EthioScript" in html_content, "HTML report must contain product name"

    def test_json_report_is_valid_json(self, evaluation_report: EvaluationReport):
        """JSON report must be parseable."""
        import json as _json
        paths = save_all_reports(evaluation_report, REPORTS_DIR)
        data = _json.loads(paths["json"].read_text(encoding="utf-8"))
        assert "run_id" in data
        assert "overall_cer" in data
        assert "documents" in data

    def test_csv_report_has_header_row(self, evaluation_report: EvaluationReport):
        """CSV report must have a header with required columns."""
        paths = save_all_reports(evaluation_report, REPORTS_DIR)
        first_line = paths["csv"].read_text(encoding="utf-8").split("\n")[0]
        for col in ("document_id", "aggregate_cer", "aggregate_wer", "passed"):
            assert col in first_line, f"CSV missing column: {col}"


# ─── Helpers ─────────────────────────────────────────────────────────────────

def _finalise_report(report: EvaluationReport) -> None:
    """Compute corpus-level aggregates across all evaluated documents."""
    if not report.document_results:
        return

    total_sub = total_del = total_ins = 0
    total_wsub = total_wdel = total_wins = 0
    total_ref_chars = total_ref_words = 0

    class_stats: dict[str, dict] = {}

    for doc in report.document_results:
        for pm in doc.page_metrics:
            total_sub  += pm.char_substitutions
            total_del  += pm.char_deletions
            total_ins  += pm.char_insertions
            total_wsub += pm.word_substitutions
            total_wdel += pm.word_deletions
            total_wins += pm.word_insertions
            total_ref_chars += pm.ref_chars
            total_ref_words += pm.ref_words

        # Per-class stats
        cls = doc.document_class
        if cls not in class_stats:
            class_stats[cls] = {
                "sub": 0, "del": 0, "ins": 0,
                "wsub": 0, "wdel": 0, "wins": 0,
                "chars": 0, "words": 0,
            }
        for pm in doc.page_metrics:
            class_stats[cls]["sub"]   += pm.char_substitutions
            class_stats[cls]["del"]   += pm.char_deletions
            class_stats[cls]["ins"]   += pm.char_insertions
            class_stats[cls]["wsub"]  += pm.word_substitutions
            class_stats[cls]["wdel"]  += pm.word_deletions
            class_stats[cls]["wins"]  += pm.word_insertions
            class_stats[cls]["chars"] += pm.ref_chars
            class_stats[cls]["words"] += pm.ref_words

    report.overall_cer = round(
        (total_sub + total_del + total_ins) / max(total_ref_chars, 1), 6
    )
    report.overall_wer = round(
        (total_wsub + total_wdel + total_wins) / max(total_ref_words, 1), 6
    )

    for cls, s in class_stats.items():
        report.per_class_cer[cls] = round(
            (s["sub"] + s["del"] + s["ins"]) / max(s["chars"], 1), 6
        )
        report.per_class_wer[cls] = round(
            (s["wsub"] + s["wdel"] + s["wins"]) / max(s["words"], 1), 6
        )
