// Package middleware provides HTTP middleware for the notification-service.
package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/ethioscript/notification-service/internal/domain"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type contextKey string

const (
	CtxUserID   contextKey = "user_id"
	CtxUserRole contextKey = "user_role"
	CtxTenantID contextKey = "tenant_id"
	CtxTraceID  contextKey = "trace_id"
)

// ─── RequestID ────────────────────────────────────────────────────────────────

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid := r.Header.Get("X-Trace-ID")
		if tid == "" {
			tid = uuid.New().String()
		}
		ctx := context.WithValue(r.Context(), CtxTraceID, tid)
		w.Header().Set("X-Trace-ID", tid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ─── Logger ───────────────────────────────────────────────────────────────────

func Logger(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(ww, r)
			log.Info("request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.status),
				zap.Duration("duration", time.Since(start)),
				zap.String("ip", r.RemoteAddr),
				zap.String("trace_id", TraceID(r.Context())),
			)
		})
	}
}

// ─── Recoverer ────────────────────────────────────────────────────────────────

func Recoverer(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered", zap.Any("panic", rec))
					WriteError(w, r, http.StatusInternalServerError, "internal server error", "PANIC")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// ─── SecureHeaders ────────────────────────────────────────────────────────────

func SecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		next.ServeHTTP(w, r)
	})
}

// ─── Authenticate — parses Bearer JWT ─────────────────────────────────────────

func Authenticate(jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				WriteError(w, r, http.StatusUnauthorized, "bearer token required", "UNAUTHORIZED")
				return
			}
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

			token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return []byte(jwtSecret), nil
			})
			if err != nil || !token.Valid {
				WriteError(w, r, http.StatusUnauthorized, "invalid or expired token", "UNAUTHORIZED")
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				WriteError(w, r, http.StatusUnauthorized, "invalid token claims", "UNAUTHORIZED")
				return
			}

			// Support both "uid" (EthioScript format) and "sub" (standard)
			userIDStr, _ := claims["uid"].(string)
			if userIDStr == "" {
				userIDStr, _ = claims["sub"].(string)
			}
			if userIDStr == "" {
				WriteError(w, r, http.StatusUnauthorized, "token missing user id", "UNAUTHORIZED")
				return
			}

			userID, err := uuid.Parse(userIDStr)
			if err != nil {
				WriteError(w, r, http.StatusUnauthorized, "invalid user id in token", "UNAUTHORIZED")
				return
			}

			role, _ := claims["role"].(string)
			tenantIDStr, _ := claims["tid"].(string)

			ctx := r.Context()
			ctx = context.WithValue(ctx, CtxUserID, userID)
			ctx = context.WithValue(ctx, CtxUserRole, domain.UserRole(role))
			if tenantIDStr != "" {
				if tid, err := uuid.Parse(tenantIDStr); err == nil {
					ctx = context.WithValue(ctx, CtxTenantID, tid)
				}
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ─── RequireRole ──────────────────────────────────────────────────────────────

func RequireRole(role domain.UserRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, _ := r.Context().Value(CtxUserRole).(domain.UserRole)
			if got != role {
				WriteError(w, r, http.StatusForbidden, "insufficient role", "FORBIDDEN")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ─── RequireInternalKey ───────────────────────────────────────────────────────

func RequireInternalKey(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Internal-Key") != key {
				WriteError(w, r, http.StatusUnauthorized, "invalid or missing X-Internal-Key", "UNAUTHORIZED")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ─── Context helpers ──────────────────────────────────────────────────────────

func UserID(ctx context.Context) uuid.UUID {
	v, _ := ctx.Value(CtxUserID).(uuid.UUID)
	return v
}

func UserRole(ctx context.Context) domain.UserRole {
	v, _ := ctx.Value(CtxUserRole).(domain.UserRole)
	return v
}

func TraceID(ctx context.Context) string {
	v, _ := ctx.Value(CtxTraceID).(string)
	return v
}

// ─── Response helpers ─────────────────────────────────────────────────────────

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, msg, code string) {
	WriteJSON(w, status, domain.ErrorResponse{
		Error:   msg,
		Code:    code,
		TraceID: TraceID(r.Context()),
	})
}

// ─── responseWriter captures status code ─────────────────────────────────────

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}
