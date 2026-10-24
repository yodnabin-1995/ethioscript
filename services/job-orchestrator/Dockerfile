# ── Stage 1: build ────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" \
    -o /app/job-orchestrator ./cmd/server

# ── Stage 2: production ───────────────────────────────────────────────────────
FROM alpine:3.19 AS production

RUN apk add --no-cache ca-certificates tzdata curl

WORKDIR /app
COPY --from=builder /app/job-orchestrator .

RUN addgroup -g 1001 appgroup && \
    adduser -u 1001 -G appgroup -s /bin/sh -D appuser && \
    chown -R appuser:appgroup /app
USER appuser

EXPOSE 6002

HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=3 \
    CMD curl -sf http://localhost:6002/health || exit 1

ENTRYPOINT ["/app/job-orchestrator"]
