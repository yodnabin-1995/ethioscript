# ── Stage 1: build ────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app
COPY go.mod ./
RUN touch go.sum && go mod download || true
COPY . .
RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" \
    -o /app/account-service ./cmd/server

# ── Stage 2: production ───────────────────────────────────────────────────────
FROM alpine:3.19 AS production

# curl needed for healthcheck; ca-certificates for HTTPS; tzdata for time zones
RUN apk add --no-cache ca-certificates tzdata curl

WORKDIR /app
COPY --from=builder /app/account-service .

RUN addgroup -g 1001 appgroup && \
    adduser -u 1001 -G appgroup -s /bin/sh -D appuser && \
    chown -R appuser:appgroup /app
USER appuser

EXPOSE 6000

HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=3 \
    CMD curl -sf http://localhost:6000/health || exit 1

ENTRYPOINT ["/app/account-service"]
