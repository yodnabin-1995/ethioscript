# E2E Tests

Full end-to-end tests that run against all running services.

## Prerequisites

```bash
make up          # starts everything
make ps          # verify all containers are healthy
```

## Run

```bash
cd tests/e2e
go test ./... -v -tags=e2e -timeout=120s
```

Or with custom URLs:

```bash
ACCOUNT_SERVICE_URL=http://localhost:6000 \
DOCUMENT_SERVICE_URL=http://localhost:6001 \
JOB_SERVICE_URL=http://localhost:6002 \
go test ./... -v -tags=e2e -timeout=120s
```

## What is tested

| Test | Covers |
|------|--------|
| `TestE2E_HealthChecks` | All 3 services respond healthy |
| `TestE2E_AuthWorkflow` | Register → profile → update → refresh → rotation → logout → login |
| `TestE2E_APIKeyLifecycle` | Create → list → revoke → verify gone |
| `TestE2E_DocumentWorkflow` | Create document → list (tenant-scoped) |
| `TestE2E_ForgotPasswordDoesNotLeak` | Forgot password never reveals email existence |
| `TestE2E_InvalidLoginReturns401` | Wrong credentials always 401 |
| `TestE2E_ProtectedEndpointRequiresAuth` | All protected routes reject unauthenticated requests |
