# ── Stage 1: build ────────────────────────────────────────────────────────────
# Pinned digest matches the cached layer from account-service build — avoids Docker Hub TLS auth
FROM golang:1.22-alpine@sha256:1699c10032ca2582ec89a24a1312d986a3f094aed3d5c1147b19880afe40e052 AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" \
    -o /app/notification-service ./cmd/server

# ── Stage 2: production ───────────────────────────────────────────────────────
FROM alpine:3.19@sha256:6baf43584bcb78f2e5847d1de515f23499913ac9f12bdf834811a3145eb11ca1 AS production

# curl for healthcheck; ca-certificates for HTTPS; tzdata for time zones
RUN apk add --no-cache ca-certificates tzdata curl

WORKDIR /app
COPY --from=builder /app/notification-service .
COPY --from=builder /app/cmd/server/docs/openapi.yml ./docs/openapi.yml

RUN addgroup -g 1001 appgroup && \
    adduser -u 1001 -G appgroup -s /bin/sh -D appuser && \
    chown -R appuser:appgroup /app
USER appuser

EXPOSE 6007

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD curl -sf http://localhost:6007/health || exit 1

ENTRYPOINT ["/app/notification-service"]
