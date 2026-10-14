package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/ethioscript/account-service/internal/auth"
	"github.com/ethioscript/account-service/internal/domain"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type contextKey string

const (
	KeyClaims  contextKey = "claims"
	KeyTraceID contextKey = "trace_id"
)

// RequestID injects a unique trace ID into each request.
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

// GetTraceID returns the trace ID stored in context.
func GetTraceID(ctx context.Context) string {
	if v, ok := ctx.Value(KeyTraceID).(string); ok {
		return v
	}
	return ""
}

// Logger logs every request with method, path, status, and duration.
func Logger(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			log.Info("request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", rw.status),
				zap.Duration("duration", time.Since(start)),
				zap.String("trace_id", GetTraceID(r.Context())),
				zap.String("ip", r.RemoteAddr),
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
					log.Error("panic recovered", zap.Any("panic", rec))
					WriteError(w, r, http.StatusInternalServerError, "internal server error", "INTERNAL_ERROR")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// SecureHeaders sets defensive HTTP headers.
func SecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// Authenticate validates the Bearer JWT and injects claims into context.
func Authenticate(jwtMgr *auth.Manager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				WriteError(w, r, http.StatusUnauthorized, "missing or malformed authorization header", "AUTH_MISSING")
				return
			}
			claims, err := jwtMgr.ValidateAccessToken(strings.TrimPrefix(header, "Bearer "))
			if err != nil {
				WriteError(w, r, http.StatusUnauthorized, "invalid or expired token", "AUTH_INVALID")
				return
			}
			ctx := context.WithValue(r.Context(), KeyClaims, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetClaims retrieves authenticated claims from context.
func GetClaims(ctx context.Context) *domain.Claims {
	if c, ok := ctx.Value(KeyClaims).(*domain.Claims); ok {
		return c
	}
	return nil
}

// RequireRole rejects requests whose role is not in the allowed set.
func RequireRole(roles ...domain.UserRole) func(http.Handler) http.Handler {
	allowed := make(map[domain.UserRole]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "AUTH_REQUIRED")
				return
			}
			if _, ok := allowed[claims.Role]; !ok {
				WriteError(w, r, http.StatusForbidden, "insufficient permissions", "FORBIDDEN")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// InternalOnly allows only requests with the internal API key header.
func InternalOnly(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Internal-Key") != apiKey {
				WriteError(w, r, http.StatusForbidden, "internal endpoint", "FORBIDDEN")
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

func WriteError(w http.ResponseWriter, r *http.Request, status int, message, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(domain.ErrorResponse{
		Error:   message,
		Code:    code,
		TraceID: GetTraceID(r.Context()),
	})
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
