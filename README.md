# EthioScript & Voice AI

Ethiopic-script document intelligence and regional-language speech transcription platform.

---

## Service Map

| Port | Service | Stack | Description |
|------|---------|-------|-------------|
| **6000** | **account-service** | Go + chi + JWT | Auth, users, tenants, API keys |
| 6001 | document-service | Go + chi | Document lifecycle, OCR jobs |
| 6002 | job-orchestrator | Go + chi | Job state, outbox, retries |
| 6003 | ocr-worker metrics | Python | Prometheus metrics endpoint |
| 6004 | stt-worker metrics | Python | Prometheus metrics endpoint |
| **6005** | **swagger-ui** | Nginx | Aggregated API docs (all 3 specs) |
| 6100 | pgadmin | — | PostgreSQL admin UI |
| 6200 | prometheus | — | Metrics scraper |
| 6201 | grafana | — | Dashboards |
| 6300 | postgres | — | PostgreSQL 16 |
| 6301 | redis | — | Redis 7 |
| 6302 | minio API | — | Object storage |
| 6303 | minio console | — | Object storage UI |
| 6304 | rabbitmq AMQP | — | Message broker |
| 6305 | rabbitmq mgmt | — | Broker management UI |

---

## Quick Start

```bash
# 1. Set up environment
cp .env.example .env
# Edit .env — change ALL passwords and JWT_SECRET (must be 32+ chars)

# 2. Start everything
make up

# 3. Show all URLs
make urls
```

---

## Route Structure (no /api/v1 prefix)

All services use clean, flat routes:

### Account Service `:6000`
```
POST   /auth/register          Register new user
POST   /auth/login             Login → JWT + refresh token
POST   /auth/refresh           Rotate refresh token
POST   /auth/logout            Revoke refresh token (current device)
POST   /auth/logout-all        Revoke all sessions
GET    /auth/me                Get profile
PUT    /auth/me                Update profile
POST   /auth/change-password   Change password
POST   /auth/forgot-password   Request password reset email
POST   /auth/reset-password    Reset password with token
POST   /auth/send-verification Resend verification email
POST   /auth/verify-email      Verify email address

POST   /api-keys               Create API key (shown once)
GET    /api-keys               List API keys
DELETE /api-keys/{id}          Revoke API key

GET    /users                  List users in tenant  [ADMIN]
GET    /users/{id}             Get user              [ADMIN]
PUT    /users/{id}/role        Change user role      [ADMIN]
POST   /users/{id}/suspend     Suspend user          [ADMIN]
POST   /users/{id}/activate    Activate user         [ADMIN]
DELETE /users/{id}             Delete user           [ADMIN]

POST   /tenants                Create tenant         [PLATFORM_OPERATOR]
GET    /tenants                List tenants          [PLATFORM_OPERATOR]
GET    /tenants/{id}           Get tenant            [PLATFORM_OPERATOR]
```

### Document Service `:6001`
```
POST   /documents                     Create document + processing job
GET    /documents                     List documents (paginated)
GET    /documents/{id}                Get document
DELETE /documents/{id}                Soft delete
PUT    /documents/{id}/result/draft   Save reviewer draft
POST   /documents/{id}/approve        Approve result
```

### Job Orchestrator `:6002`
```
GET    /jobs/{id}       Get job status + model provenance
POST   /jobs/{id}/cancel  Cancel job
```

---

## First API Call

```bash
# 1. Register
curl -s -X POST http://localhost:6000/auth/register \
  -H "Content-Type: application/json" \
  -d '{"tenant_slug":"default","name":"Abebe","email":"abebe@test.com","password":"Test1234"}' | jq

# 2. Login
TOKEN=$(curl -s -X POST http://localhost:6000/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"abebe@test.com","password":"Test1234"}' | jq -r '.access_token')

# 3. Get profile
curl -s http://localhost:6000/auth/me \
  -H "Authorization: Bearer $TOKEN" | jq

# 4. Create API key
curl -s -X POST http://localhost:6000/api-keys \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"My key","scopes":["documents:read","documents:write"]}' | jq
```

---

## Swagger UI

| URL | Description |
|-----|-------------|
| **http://localhost:6005** | Aggregated — tab between Account, Document, Job specs |
| http://localhost:6000/docs | Account Service embedded Swagger |
| http://localhost:6001/docs | Document Service embedded Swagger |
| http://localhost:6002/docs | Job Orchestrator embedded Swagger |

---

## Architecture

```
                    ┌─────────────────────┐
Browser / App  ───▶ │  account-service    │  :6000
                    │  (auth, users,       │
                    │   tenants, api-keys) │
                    └─────────────────────┘
                              │ X-Tenant-ID / X-User-ID / X-Internal-Key
              ┌───────────────┴────────────────┐
              ▼                                 ▼
 ┌─────────────────────┐          ┌──────────────────────────┐
 │  document-service   │  :6001   │  job-orchestrator        │  :6002
 │  (lifecycle, OCR    │          │  (state machine, outbox, │
 │   jobs, approval)   │          │   retry, dead-letter)    │
 └─────────────────────┘          └──────────────────────────┘
              │ RabbitMQ                        │ RabbitMQ
              └──────────┬─────────────────────┘
                         │
           ┌─────────────┴──────────────┐
           ▼                             ▼
  ┌────────────────┐          ┌──────────────────┐
  │  ocr-worker    │ :6003    │  stt-worker      │ :6004
  │  (Tesseract 5  │          │  (Whisper,       │
  │   Amharic OCR) │          │   Release 2)     │
  └────────────────┘          └──────────────────┘
           │                             │
           └─────────────┬───────────────┘
                         ▼
              ┌──────────────────┐
              │  job-orchestrator│  (result handler)
              └──────────────────┘

Shared infrastructure:
  PostgreSQL :6300 · Redis :6301 · MinIO :6302 · RabbitMQ :6304
```

---

## Useful Commands

```bash
make up             # Start everything
make down           # Stop everything
make logs           # Tail all logs
make logs-account   # Tail account-service logs
make logs-doc       # Tail document-service logs
make ps             # Show container status
make shell-db       # Open psql
make urls           # Print all URLs
make rebuild        # Force rebuild all images
```

---

*EthioScript & Voice AI — SRS v3.1.0 | Architecture: Microservices + Hexagonal*
