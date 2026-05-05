package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func Auth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublicRoute(r) {
				next.ServeHTTP(w, r)
				return
			}

			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				WriteError(w, r, http.StatusUnauthorized, "auth_required", "missing bearer token", nil)
				return
			}
			got := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				WriteError(w, r, http.StatusUnauthorized, "auth_invalid", "invalid bearer token", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isPublicRoute(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case "/api/v1/health", "/api/v1/version", "/api/v1/openapi.yaml":
		return true
	default:
		return false
	}
}
