# Test Images

Place evaluation test images here.

Each image filename **must match** a ground truth file in `../ground_truth/`.

## Supported formats

| Extension | Notes |
|---|---|
| `.pdf` | Multi-page PDFs supported — each page is evaluated separately |
| `.png` | Preferred for synthetic/generated images |
| `.jpg` / `.jpeg` | Acceptable for scanned documents |
| `.tiff` | Good for high-DPI archival scans |
| `.webp` | Supported |

## Naming convention

```
{language}_{document_class}_{sequence_number}.{ext}
```

Examples:
```
amharic_printed_001.png
amharic_handwritten_001.pdf
tigrinya_printed_001.tiff
amharic_degraded_001.jpg
```

## DPI guidelines

- Minimum recommended: 200 DPI
- Target for OCR quality: 300 DPI
- High-detail documents (small print): 400 DPI

## Current fixtures

This directory is currently empty — add images and their matching
ground truth files to begin evaluation.

Run `python -m pytest test_ocr_evaluation.py -v` from the `ai-evaluation/`
directory. If no real images are present, synthetic fixtures are auto-generated.
