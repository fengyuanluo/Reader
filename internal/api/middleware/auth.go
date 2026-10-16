package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Auth 校验 Bearer Token。token 为空表示关闭鉴权：serve 启动时已保证空 token
// 只能监听回环地址（config.RequireTokenForAddr），此时直接放行。
// scheme 按 RFC 7235 大小写不敏感；凭据严格比较，不做任何空白裁剪。
func Auth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if token == "" {
			return next
		}
		want := []byte(token)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublicRoute(r) {
				next.ServeHTTP(w, r)
				return
			}

			scheme, credential, ok := strings.Cut(r.Header.Get("Authorization"), " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") {
				WriteError(w, r, http.StatusUnauthorized, "auth_required", "missing bearer token", nil)
				return
			}
			if subtle.ConstantTimeCompare([]byte(credential), want) != 1 {
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
