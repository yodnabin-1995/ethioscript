// Package middleware provides shared HTTP middleware for document-service.
package middleware

import (
	"encoding/json"
	"net/http"
)

// InternalOnly rejects requests that do not carry the correct X-Internal-Key header.
// This prevents unauthenticated callers from directly hitting internal service ports.
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
