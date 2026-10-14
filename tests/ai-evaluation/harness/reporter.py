"""
reporter.py — Generates evaluation reports from DocumentMetrics results.

Outputs:
  - reports/evaluation_<run_id>.json  (machine-readable, for CI)
  - reports/evaluation_<run_id>.html  (human-readable, with charts)
  - reports/evaluation_<run_id>.csv   (import into spreadsheets)
  - reports/latest_summary.txt        (always overwritten — quick CI check)

SRS 7.3: Publish measured CER/WER and task-specific results on
         documented test sets instead of generic accuracy claims.
"""

import csv
import json
import logging
import os
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional

from harness.metrics import DocumentMetrics, EvaluationReport

logger = logging.getLogger(__name__)

REPORTS_DIR = Path(__file__).parent.parent / "reports"


# ─── JSON report ──────────────────────────────────────────────────────────────

def save_json_report(report: EvaluationReport, output_dir: Path = REPORTS_DIR) -> Path:
    """Save full evaluation report as JSON."""
    output_dir.mkdir(parents=True, exist_ok=True)
    path = output_dir / f"evaluation_{report.run_id}.json"

    # Convert dataclasses to dicts for JSON serialisation
    data = {
        "run_id":             report.run_id,
        "timestamp":          report.timestamp,
        "model_name":         report.model_name,
        "model_version":      report.model_version,
        "language":           report.language,
        "document_class":     report.document_class,
        "cer_threshold":      report.cer_threshold,
        "overall_cer":        report.overall_cer,
        "overall_wer":        report.overall_wer,
        "total_documents":    report.total_documents,
        "passed_documents":   report.passed_documents,
        "failed_documents":   report.failed_documents,
        "per_class_cer":      report.per_class_cer,
        "per_class_wer":      report.per_class_wer,
        "notes":              report.notes,
        "documents": [
            {
                "document_id":     d.document_id,
                "language":        d.language,
                "document_class":  d.document_class,
                "model_name":      d.model_name,
                "model_version":   d.model_version,
                "aggregate_cer":   d.aggregate_cer,
                "aggregate_wer":   d.aggregate_wer,
                "page_count":      d.page_count,
                "failed_pages":    d.failed_pages,
                "total_ref_chars": d.total_ref_chars,
                "total_ref_words": d.total_ref_words,
                "processing_secs": d.processing_seconds,
                "pages": [
                    {
                        "page":       p.page_number,
                        "cer":        p.cer,
                        "wer":        p.wer,
                        "ref_chars":  p.ref_chars,
                        "ref_words":  p.ref_words,
                        "char_sub":   p.char_substitutions,
                        "char_del":   p.char_deletions,
                        "char_ins":   p.char_insertions,
                    }
                    for p in d.page_metrics
                ],
            }
            for d in report.document_results
        ],
    }

    path.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
    logger.info("JSON report → %s", path)
    return path


# ─── CSV report ───────────────────────────────────────────────────────────────

def save_csv_report(report: EvaluationReport, output_dir: Path = REPORTS_DIR) -> Path:
    """Save per-document results as CSV."""
    output_dir.mkdir(parents=True, exist_ok=True)
    path = output_dir / f"evaluation_{report.run_id}.csv"

    rows = []
    for d in report.document_results:
        rows.append({
            "run_id":          report.run_id,
            "document_id":     d.document_id,
            "language":        d.language,
            "document_class":  d.document_class,
            "model_name":      d.model_name,
            "model_version":   d.model_version,
            "aggregate_cer":   f"{d.aggregate_cer:.4f}",
            "aggregate_wer":   f"{d.aggregate_wer:.4f}",
            "passed":          "YES" if d.aggregate_cer <= report.cer_threshold else "NO",
            "page_count":      d.page_count,
            "total_ref_chars": d.total_ref_chars,
            "total_ref_words": d.total_ref_words,
            "processing_secs": f"{d.processing_seconds:.2f}",
        })

    if rows:
        with open(path, "w", newline="", encoding="utf-8") as f:
            writer = csv.DictWriter(f, fieldnames=rows[0].keys())
            writer.writeheader()
            writer.writerows(rows)

    logger.info("CSV report → %s", path)
    return path


# ─── Text summary ─────────────────────────────────────────────────────────────

def save_text_summary(report: EvaluationReport, output_dir: Path = REPORTS_DIR) -> Path:
    """Save a short human-readable summary (also written to latest_summary.txt)."""
    output_dir.mkdir(parents=True, exist_ok=True)
    path     = output_dir / f"summary_{report.run_id}.txt"
    latest   = output_dir / "latest_summary.txt"

    passed   = report.passed_documents
    total    = report.total_documents
    pct      = (passed / total * 100) if total > 0 else 0.0
    threshold_pct = report.cer_threshold * 100

    lines = [
        "=" * 64,
        "  EthioScript AI Evaluation Report",
        "=" * 64,
        f"  Run ID       : {report.run_id}",
        f"  Timestamp    : {report.timestamp}",
        f"  Model        : {report.model_name} v{report.model_version}",
        f"  Language     : {report.language}",
        f"  Doc class    : {report.document_class}",
        "-" * 64,
        f"  Overall CER  : {report.overall_cer * 100:.2f}%  "
        f"(threshold: {threshold_pct:.1f}%)",
        f"  Overall WER  : {report.overall_wer * 100:.2f}%",
        f"  Documents    : {passed}/{total} passed ({pct:.1f}%)",
        "-" * 64,
    ]

    # Per-class breakdown
    if report.per_class_cer:
        lines.append("  Per-class CER:")
        for cls, cer in sorted(report.per_class_cer.items()):
            status = "✓" if cer <= report.cer_threshold else "✗"
            lines.append(f"    {status}  {cls:<20} {cer * 100:.2f}%")
        lines.append("-" * 64)

    # Per-document table
    lines.append(f"  {'Document':<40} {'CER':>8} {'WER':>8} {'Pass':>6}")
    lines.append("  " + "-" * 60)
    for d in report.document_results:
        ok = "✓" if d.aggregate_cer <= report.cer_threshold else "✗"
        lines.append(
            f"  {ok} {d.document_id:<38} "
            f"{d.aggregate_cer * 100:>7.2f}% "
            f"{d.aggregate_wer * 100:>7.2f}%"
        )

    lines.append("=" * 64)
    if report.notes:
        lines.append("  Notes:")
        for note in report.notes:
            lines.append(f"    • {note}")
        lines.append("=" * 64)

    content = "\n".join(lines) + "\n"
    path.write_text(content, encoding="utf-8")
    latest.write_text(content, encoding="utf-8")
    logger.info("Summary → %s", path)
    return path


# ─── HTML report ──────────────────────────────────────────────────────────────

def save_html_report(report: EvaluationReport, output_dir: Path = REPORTS_DIR) -> Path:
    """Save a styled HTML evaluation report with charts."""
    output_dir.mkdir(parents=True, exist_ok=True)
    path = output_dir / f"evaluation_{report.run_id}.html"

    passed_pct    = (report.passed_documents / max(report.total_documents, 1)) * 100
    threshold_pct = report.cer_threshold * 100
    overall_cer   = report.overall_cer * 100
    overall_wer   = report.overall_wer * 100
    pass_color    = "#38a169" if report.overall_cer <= report.cer_threshold else "#e53e3e"

    doc_rows = ""
    for d in report.document_results:
        ok    = "✓" if d.aggregate_cer <= report.cer_threshold else "✗"
        color = "#38a169" if d.aggregate_cer <= report.cer_threshold else "#e53e3e"
        doc_rows += f"""
        <tr>
          <td style="color:{color};font-weight:700">{ok}</td>
          <td>{d.document_id}</td>
          <td>{d.language}</td>
          <td>{d.document_class}</td>
          <td>{d.aggregate_cer * 100:.2f}%</td>
          <td>{d.aggregate_wer * 100:.2f}%</td>
          <td>{d.page_count}</td>
          <td>{d.processing_seconds:.2f}s</td>
        </tr>"""

    class_rows = ""
    for cls, cer in sorted(report.per_class_cer.items()):
        wer   = report.per_class_wer.get(cls, 0.0)
        ok    = "✓" if cer <= report.cer_threshold else "✗"
        color = "#38a169" if cer <= report.cer_threshold else "#e53e3e"
        class_rows += f"""
        <tr>
          <td style="color:{color};font-weight:700">{ok}</td>
          <td>{cls}</td>
          <td>{cer * 100:.2f}%</td>
          <td>{wer * 100:.2f}%</td>
        </tr>"""

    html = f"""<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8"/>
  <title>EthioScript AI Evaluation — {report.run_id}</title>
  <style>
    body{{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;
         margin:0;background:#f7fafc;color:#2d3748}}
    .header{{background:linear-gradient(135deg,#1a3a5c,#2c5282);color:white;
             padding:24px 40px}}
    .header h1{{margin:0;font-size:1.5rem}}
    .header p{{margin:4px 0 0;opacity:.8;font-size:.9rem}}
    .container{{max-width:1100px;margin:0 auto;padding:24px 40px}}
    .kpi-row{{display:flex;gap:16px;margin:20px 0}}
    .kpi{{flex:1;background:white;border-radius:8px;padding:20px;
          box-shadow:0 1px 3px rgba(0,0,0,.1);text-align:center}}
    .kpi .value{{font-size:2rem;font-weight:700;color:{pass_color}}}
    .kpi .label{{font-size:.85rem;color:#718096;margin-top:4px}}
    table{{width:100%;border-collapse:collapse;background:white;
           border-radius:8px;overflow:hidden;
           box-shadow:0 1px 3px rgba(0,0,0,.1);margin:16px 0}}
    th{{background:#2d3748;color:white;padding:10px 14px;text-align:left;
        font-size:.85rem;font-weight:600}}
    td{{padding:9px 14px;border-bottom:1px solid #e2e8f0;font-size:.9rem}}
    tr:last-child td{{border-bottom:none}}
    tr:hover td{{background:#f7fafc}}
    h2{{font-size:1.1rem;color:#2d3748;margin:28px 0 8px}}
    .notes{{background:#fffbeb;border-left:4px solid #f6ad55;
            padding:12px 16px;border-radius:0 6px 6px 0;font-size:.9rem}}
  </style>
</head>
<body>
<div class="header">
  <h1>🇪🇹 EthioScript AI Evaluation Report</h1>
  <p>Run: {report.run_id} | {report.timestamp} |
     Model: {report.model_name} v{report.model_version} |
     Language: {report.language} | Class: {report.document_class}</p>
</div>
<div class="container">
  <div class="kpi-row">
    <div class="kpi">
      <div class="value">{overall_cer:.2f}%</div>
      <div class="label">Overall CER<br/>(threshold: {threshold_pct:.1f}%)</div>
    </div>
    <div class="kpi">
      <div class="value">{overall_wer:.2f}%</div>
      <div class="label">Overall WER</div>
    </div>
    <div class="kpi">
      <div class="value">{report.passed_documents}/{report.total_documents}</div>
      <div class="label">Documents Passed ({passed_pct:.1f}%)</div>
    </div>
    <div class="kpi">
      <div class="value">{report.total_documents}</div>
      <div class="label">Total Documents Evaluated</div>
    </div>
  </div>

  <h2>Per-Class Results</h2>
  <table>
    <tr><th></th><th>Document Class</th><th>CER</th><th>WER</th></tr>
    {class_rows if class_rows else '<tr><td colspan="4">No class data</td></tr>'}
  </table>

  <h2>Per-Document Results</h2>
  <table>
    <tr>
      <th></th><th>Document</th><th>Language</th><th>Class</th>
      <th>CER</th><th>WER</th><th>Pages</th><th>Time</th>
    </tr>
    {doc_rows if doc_rows else '<tr><td colspan="8">No documents evaluated</td></tr>'}
  </table>

  {f'<h2>Notes</h2><div class="notes"><ul>' + "".join(f"<li>{n}</li>" for n in report.notes) + "</ul></div>" if report.notes else ""}
</div>
</body>
</html>"""

    path.write_text(html, encoding="utf-8")
    logger.info("HTML report → %s", path)
    return path


# ─── All-in-one save ──────────────────────────────────────────────────────────

def save_all_reports(report: EvaluationReport, output_dir: Path = REPORTS_DIR) -> dict[str, Path]:
    """Save JSON, CSV, text summary, and HTML reports. Returns paths dict."""
    return {
        "json":    save_json_report(report, output_dir),
        "csv":     save_csv_report(report, output_dir),
        "summary": save_text_summary(report, output_dir),
        "html":    save_html_report(report, output_dir),
    }
