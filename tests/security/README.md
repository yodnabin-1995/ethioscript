# Security Tests

Adversarial tests verifying authentication, authorization, tenant isolation,
input validation, rate limiting, and sensitive-data handling.

## Prerequisites

```bash
make up          # all services must be running
make ps          # verify all healthy
```

## Run

```bash
cd tests/security
go test ./... -v -tags=security -timeout=120s
```

With custom URLs:

```bash
ACCOUNT_SERVICE_URL=http://localhost:6000 \
DOCUMENT_SERVICE_URL=http://localhost:6001 \
go test ./... -v -tags=security -timeout=120s
```

## Test Coverage

| Test | Category | What it verifies |
|------|----------|-----------------|
| `TestSec_NoTokenRejected` | Auth | Every protected endpoint returns 401 with no token |
| `TestSec_MalformedTokenRejected` | Auth | Malformed / alg:none / oversized tokens all rejected |
| `TestSec_RevokedTokenRejected` | Auth | Revoked refresh token is rejected after logout-all |
| `TestSec_CrossTenantAPIKeyIsolation` | Isolation | User B cannot see or revoke User A's API keys |
| `TestSec_CrossTenantDocumentIsolation` | Isolation | Document lists are strictly tenant-scoped |
| `TestSec_ContributorCannotAccessAdminEndpoints` | RBAC | CONTRIBUTOR cannot reach TENANT_ADMIN routes |
| `TestSec_WeakPasswordRejected` | Password policy | Short / no-uppercase / no-digit passwords rejected |
| `TestSec_SQLInjectionInEmailRejected` | Injection | SQL injection strings never cause 500 errors |
| `TestSec_OversizedPayloadRejected` | Input safety | 1 MB payload does not crash the service |
| `TestSec_BruteForceLoginLocksAccount` | Lockout | Repeated wrong passwords lock the account |
| `TestSec_RateLimitEngages` | Rate limiting | Burst of 150 requests activates rate limiter |
| `TestSec_PasswordHashNotLeaked` | Data leakage | bcrypt hashes never appear in API responses |
| `TestSec_TokenHashNotLeaked` | Data leakage | key_hash never returned in API key responses |
