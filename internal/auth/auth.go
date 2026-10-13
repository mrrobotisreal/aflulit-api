// Package auth is syncd's bearer-token middleware. Every route — including
// unknown paths — sits behind it, so an unauthenticated caller learns
// nothing about the service (design §9: no unauthenticated endpoint).
package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Middleware rejects any request whose Authorization header is not exactly
// "Bearer <token>" with 401. Comparison is constant-time.
func Middleware(token string, next http.Handler) http.Handler {
	tokenBytes := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		scheme, presented, found := strings.Cut(header, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") ||
			subtle.ConstantTimeCompare([]byte(presented), tokenBytes) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
