# Build MinIO from source using Go — no external binary downloads needed
# All dependencies come from Go module proxy (sum.golang.org) which is always open

FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

# Build MinIO server
RUN go install github.com/minio/minio@RELEASE.2024-01-16T16-07-38Z 2>&1 || \
    go install github.com/minio/minio@latest

# Build mc (MinIO client)
RUN go install github.com/minio/mc@RELEASE.2024-01-13T08-44-48Z 2>&1 || \
    go install github.com/minio/mc@latest

# ── Production stage ──────────────────────────────────────────────────────────
FROM alpine:3.19

RUN apk add --no-cache ca-certificates curl

COPY --from=builder /go/bin/minio /usr/local/bin/minio
COPY --from=builder /go/bin/mc    /usr/local/bin/mc

RUN mkdir -p /data && \
    addgroup -g 1001 minio && \
    adduser -u 1001 -G minio -s /bin/sh -D miniouser && \
    chown -R miniouser:minio /data

USER miniouser

EXPOSE 9000 9001

HEALTHCHECK --interval=20s --timeout=10s --start-period=60s --retries=5 \
    CMD curl -f http://localhost:9000/minio/health/live || exit 1

ENTRYPOINT ["/usr/local/bin/minio"]
CMD ["server", "/data", "--console-address", ":9001"]
