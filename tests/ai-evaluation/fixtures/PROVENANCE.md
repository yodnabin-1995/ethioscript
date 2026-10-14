# Test Fixture Provenance

SRS Section 7.2 requirement: *"Collect samples with documented permission,
provenance, usage rights, and sensitivity classification."*

---

## Fixture Registry

| Fixture ID | Language | Class | Source | License | Permitted | Annotation |
|---|---|---|---|---|---|---|
| amharic_printed_001 | amh | printed | synthetic scaffold | Public domain | ✓ | Manual |
| synthetic_amh_simple_001 | amh | synthetic | generated (Pillow) | Public domain | ✓ | Automatic |
| synthetic_amh_sentence_001 | amh | synthetic | generated (Pillow) | Public domain | ✓ | Automatic |
| synthetic_ascii_001 | eng | synthetic | generated (Pillow) | Public domain | ✓ | Automatic |

---

## Adding Real Fixtures

When adding real institutional documents:

1. **Obtain explicit written permission** from the data owner.
2. **Record the source** — institution, document type, date, reference number.
3. **Classify sensitivity** — public / internal / confidential / restricted.
4. **Never upload** confidential material to any third-party service without
   a signed data-processing agreement (SRS 7.2).
5. Add a row to this table and a sidecar `.json` metadata file.

### Sidecar JSON format

```json
{
  "language": "amh",
  "document_class": "printed",
  "font_info": "GFF Abyssinica SIL Regular",
  "scan_dpi": 300,
  "domain": "government",
  "notes": "One-page government notice, 2019",
  "permitted": true,
  "source": "Ethiopian Federal Archives — ref ARC-2019-001",
  "license": "permission granted 2026-09-01 — see docs/data-governance/ARC-2019-001.pdf",
  "sensitivity": "public",
  "annotator": "Name — verified 2026-09-15",
  "annotation_method": "manual"
}
```

---

## Ground-Truth Annotation Guidelines

- Annotate exactly what appears in the document — including spacing and
  line breaks as they appear.
- Use Unicode NFC normalisation for all Ethiopic text.
- Do not correct spelling errors in the original — transcribe verbatim.
- Mark unclear characters with `[?]` and note in the `.json` notes field.
- Double-review a subset (≥10%) to estimate inter-annotator agreement.

---

## Train / Val / Test Split

**This directory (`test/`) is the held-out test set only.**
- Training data lives separately and is never mixed into this directory.
- Validation data is in `fixtures/val/` (not yet populated).
- Splits are made at the document/source level — no page from the same
  document appears in more than one split.

---

*Last updated: October 2026*
