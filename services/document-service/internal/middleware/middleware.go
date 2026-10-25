// Package middleware provides shared HTTP middleware for document-service.
package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type contextKey string

const KeyTraceID contextKey = "trace_id"

// RequestID injects a unique X-Trace-ID into every request.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Trace-ID")
		if traceID == "" {
			traceID = uuid.NewString()
		}
		ctx := context.WithValue(r.Context(), KeyTraceID, traceID)
		w.Header().Set("X-Trace-ID", traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetTraceID retrieves the trace ID from context.
func GetTraceID(ctx context.Context) string {
	if v, ok := ctx.Value(KeyTraceID).(string); ok {
		return v
	}
	return ""
}

// Logger logs every HTTP request: method, path, status, duration, trace_id, ip.
func Logger(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)

			level := log.Info
			if rw.status >= 500 {
				level = log.Error
			} else if rw.status >= 400 {
				level = log.Warn
			}

			level("request",
				zap.String("method",   r.Method),
				zap.String("path",     r.URL.Path),
				zap.Int("status",      rw.status),
				zap.Duration("duration", time.Since(start)),
				zap.String("trace_id", GetTraceID(r.Context())),
				zap.String("ip",       r.RemoteAddr),
				zap.String("query",    r.URL.RawQuery),
			)
		})
	}
}

// Recoverer catches panics and returns 500.
func Recoverer(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered",
						zap.Any("panic",    rec),
						zap.String("path",  r.URL.Path),
						zap.String("trace_id", GetTraceID(r.Context())),
					)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"error": "internal server error",
						"code":  "INTERNAL_ERROR",
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// InternalOnly allows only requests with the correct X-Internal-Key header.
func InternalOnly(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if apiKey != "" && r.Header.Get("X-Internal-Key") != apiKey {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "forbidden",
					"code":  "FORBIDDEN",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
