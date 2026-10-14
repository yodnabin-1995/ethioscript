# AI Evaluation — OCR/STT Quality Harness

Measures CER (Character Error Rate) and WER (Word Error Rate) for the
EthioScript OCR and STT pipelines against documented ground-truth test sets.

Implements SRS Section 7 — AI Quality, Data, and Evaluation.

---

## Directory Structure

```
tests/ai-evaluation/
├── conftest.py                      # pytest session config + logging
├── pytest.ini                       # pytest settings + HTML/JSON reporters
├── requirements.txt                 # Python deps
├── test_ocr_evaluation.py           # main pytest test file
│
├── harness/
│   ├── __init__.py
│   ├── metrics.py                   # CER/WER computation + Ethiopic normalisation
│   ├── fixtures.py                  # fixture discovery, loading, synthetic generation
│   ├── ocr_runner.py                # local Tesseract + live API runner
│   └── reporter.py                  # JSON / CSV / HTML / text report generation
│
├── fixtures/
│   ├── PROVENANCE.md                # data rights, permissions, annotation guidelines
│   ├── ground_truth/                # *.txt ground truth + *.json metadata per fixture
│   ├── test_images/                 # *.png / *.pdf / *.jpg test images
│   └── test_audio/                  # *.wav / *.mp3 (Release 2 STT)
│
└── reports/                         # generated at runtime — git-ignored
    ├── evaluation_<run_id>.json
    ├── evaluation_<run_id>.csv
    ├── evaluation_<run_id>.html
    ├── summary_<run_id>.txt
    └── latest_summary.txt           # always overwritten — quick CI check
```

---

## Quick Start

```bash
cd tests/ai-evaluation

# 1. Install dependencies
pip install -r requirements.txt

# 2. Install Tesseract + Amharic tessdata (Ubuntu/Debian)
sudo apt install tesseract-ocr tesseract-ocr-amh tesseract-ocr-eng \
                 tesseract-ocr-tir tesseract-ocr-orm

# 3. Run evaluation
python -m pytest test_ocr_evaluation.py -v

# 4. View results
open reports/evaluation_<run_id>.html
cat  reports/latest_summary.txt
```

---

## Configuration

All settings are configurable via environment variables:

| Variable | Default | Description |
|---|---|---|
| `OCR_LANGUAGE` | `amh+eng` | Tesseract language code(s) |
| `OCR_DPI` | `300` | DPI for PDF rendering |
| `OCR_PSM` | `3` | Tesseract page segmentation mode |
| `OCR_OEM` | `3` | Tesseract OCR engine mode |
| `OCR_CER_THRESHOLD` | `0.15` | Maximum acceptable CER (15%) |
| `OCR_WER_THRESHOLD` | `0.25` | Maximum acceptable WER (25%) |
| `OCR_MODEL_NAME` | `tesseract` | Model name for report |
| `OCR_MODEL_VERSION` | `5.x` | Model version for report |
| `OCR_RUN_ID` | auto UUID | Unique run identifier |
| `OCR_USE_SYNTHETIC` | `true` | Generate synthetic fixtures if none exist |

> **SRS Note (Section 15):** The CER threshold of 15% is a *proposed* placeholder.
> Per SRS acceptance criteria: *"The initial OCR release meets a numeric CER
> target agreed after baseline testing on the chosen dataset; the target is
> documented rather than guessed."* Update `OCR_CER_THRESHOLD` after measuring
> your actual baseline.

---

## Test Classes

### `TestMetricsUnit` — always runs, no Tesseract needed

| Test | What it verifies |
|---|---|
| `test_identical_text_gives_zero_cer` | CER = 0 for identical text |
| `test_identical_text_gives_zero_wer` | WER = 0 for identical text |
| `test_empty_hypothesis_gives_cer_one` | CER = 1.0 when hypothesis is empty |
| `test_single_char_substitution` | Single char error = CER 1.0 |
| `test_partial_match_cer_is_between_zero_and_one` | Partial match gives 0 < CER < 1 |
| `test_evaluate_page_returns_correct_type` | PageMetrics type check |
| `test_aggregate_document_metrics_corpus_level` | Corpus-level aggregation |
| `test_ethiopic_normalisation_strips_punctuation` | Ethiopic ፡ ። stripped |
| `test_nfc_normalisation_applied` | NFC makes composed/decomposed equal |
| `test_wer_single_word_deletion` | WER = 0.5 for one deleted word |
| `test_report_structure` | EvaluationReport dataclass valid |

### `TestFixtureDiscovery` — always runs

Verifies fixture files exist, ground truth is non-empty, and metadata is valid.

### `TestOCREvaluation` — requires Tesseract

| Test | What it verifies |
|---|---|
| `test_ocr_runs_without_error` | Pipeline completes without exception |
| `test_per_fixture_cer_within_threshold` | **CER ≤ threshold for all fixtures** |
| `test_amharic_specific_cer` | Amharic-language CER is a valid ratio |
| `test_ocr_output_is_valid_utf8_ethiopic` | No encoding corruption in output |

### `TestReportGeneration` — always runs

| Test | What it verifies |
|---|---|
| `test_generate_all_report_formats` | JSON + CSV + HTML + TXT created |
| `test_html_report_contains_cer` | HTML mentions CER |
| `test_json_report_is_valid_json` | JSON parseable with correct keys |
| `test_csv_report_has_header_row` | CSV has required column headers |

---

## Adding Test Fixtures

1. Add image to `fixtures/test_images/amharic_printed_002.png`
2. Add ground truth to `fixtures/ground_truth/amharic_printed_002.txt`
3. Add metadata to `fixtures/ground_truth/amharic_printed_002.json`
4. Update `fixtures/PROVENANCE.md` with source, license, and annotation info
5. Run evaluation — new fixture is auto-discovered

---

## CI Integration

```yaml
# .github/workflows/ai-eval.yml (example)
- name: Run AI evaluation
  run: |
    cd tests/ai-evaluation
    pip install -r requirements.txt
    python -m pytest test_ocr_evaluation.py -v \
      --json-report --json-report-file=reports/pytest_report.json
  env:
    OCR_CER_THRESHOLD: "0.15"
    OCR_LANGUAGE: "amh+eng"

- name: Upload evaluation reports
  uses: actions/upload-artifact@v4
  with:
    name: ai-evaluation-reports
    path: tests/ai-evaluation/reports/
```

---

## Metrics Reference (SRS 7.3)

```
CER = (substitutions + deletions + insertions) / reference_characters
WER = (substitutions + deletions + insertions) / reference_words
```

Normalisation applied before comparison:
1. Unicode NFC normalisation
2. Ethiopic punctuation marks stripped (፡ ። ፣ ፤)
3. Multiple whitespace collapsed to single space
4. Leading/trailing whitespace stripped
