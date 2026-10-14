# EthioScript Event Schemas

Versioned broker event envelope and payload schemas used across all services.

## Routing Keys (RabbitMQ topic exchange: `ethioscript.events`)

| Routing Key                        | Published By        | Consumed By        |
|------------------------------------|---------------------|--------------------|
| `jobs.ocr`                         | job-orchestrator    | ocr-worker         |
| `jobs.stt`                         | job-orchestrator    | stt-worker         |
| `jobs.results`                     | ocr-worker / stt-worker | job-orchestrator |
| `jobs.dead-letter`                 | job-orchestrator    | operator tooling   |
| `document.processing.requested`    | document-service    | job-orchestrator   |

## Event Envelope

Every event published to the broker has this structure:

```json
{
  "event_id":       "uuid",
  "event_type":     "document.processing.requested",
  "schema_version": 1,
  "occurred_at":    "2026-10-14T10:00:00Z",
  "tenant_id":      "uuid",
  "aggregate_id":   "uuid",
  "correlation_id": "uuid",
  "causation_id":   "uuid",
  "payload":        {}
}
```

## Payload: `document.processing.requested`

```json
{
  "document_id": "uuid",
  "job_id":      "uuid",
  "asset_id":    "tenantID/assets/assetID/filename.pdf",
  "tenant_id":   "uuid",
  "task_type":   "OCR",
  "language":    "amh",
  "media_type":  "application/pdf"
}
```

## Payload: `jobs.results` (worker result)

```json
{
  "job_id":           "uuid",
  "document_id":      "uuid",
  "tenant_id":        "uuid",
  "success":          true,
  "raw_text_key":     "tenantID/results/docID/v1/raw_text.txt",
  "model_name":       "tesseract",
  "model_version":    "5.3.4",
  "language":         "amh+eng",
  "confidence_score": 0.87,
  "page_count":       3,
  "word_count":       412,
  "char_count":       2890,
  "processed_at":     "2026-10-14T10:05:00Z"
}
```
