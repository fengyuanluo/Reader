package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthAllowsPublicRoutesWithoutToken(t *testing.T) {
	routes := []string{
		"/api/v1/health",
		"/api/v1/version",
		"/api/v1/openapi.yaml",
	}

	for _, route := range routes {
		t.Run(route, func(t *testing.T) {
			handler := Auth("secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))

			req := httptest.NewRequest(http.MethodGet, route, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", rec.Code)
			}
		})
	}
}

func TestAuthRejectsMissingToken(t *testing.T) {
	handler := Trace(Auth("secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("protected handler should not be called")
	})))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/book-sources/import", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assertError(t, rec, http.StatusUnauthorized, "auth_required")
}

func TestAuthRejectsInvalidToken(t *testing.T) {
	handler := Trace(Auth("secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("protected handler should not be called")
	})))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/book-sources/import", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assertError(t, rec, http.StatusUnauthorized, "auth_invalid")
}

func TestAuthAllowsValidToken(t *testing.T) {
	handler := Auth("secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/book-sources/import", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}

func TestAuthBearerParsing(t *testing.T) {
	tests := []struct {
		name   string
		header string
		code   string // 空表示放行
	}{
		{name: "canonical", header: "Bearer secret"},
		{name: "lowercase scheme", header: "bearer secret"},
		{name: "uppercase scheme", header: "BEARER secret"},
		{name: "trailing nbsp", header: "Bearer secret\u00a0", code: "auth_invalid"},
		{name: "trailing nel", header: "Bearer secret\u0085", code: "auth_invalid"},
		{name: "nbsp only", header: "Bearer \u00a0", code: "auth_invalid"},
		{name: "empty credential", header: "Bearer ", code: "auth_invalid"},
		{name: "wrong token", header: "Bearer wrong", code: "auth_invalid"},
		{name: "basic scheme", header: "Basic secret", code: "auth_required"},
		{name: "no separator", header: "Bearersecret", code: "auth_required"},
		{name: "scheme only", header: "Bearer", code: "auth_required"},
		{name: "missing", header: "", code: "auth_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := Trace(Auth("secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})))
			req := httptest.NewRequest(http.MethodPost, "/api/v1/book-sources/import", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if tt.code == "" {
				if rec.Code != http.StatusNoContent {
					t.Fatalf("status = %d, want 204", rec.Code)
				}
				return
			}
			assertError(t, rec, http.StatusUnauthorized, tt.code)
		})
	}
}

// 空 token 仅在回环监听下可达（serve 启动校验），中间件直接放行，不再依赖空串比较。
func TestAuthEmptyTokenPassesThrough(t *testing.T) {
	handler := Auth("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, header := range []string{"", "Bearer \u00a0", "Bearer anything"} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/book-sources/import", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("header %q: status = %d, want 204", header, rec.Code)
		}
	}
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d", rec.Code, status)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != code {
		t.Fatalf("code = %v, want %s", body["code"], code)
	}
	if body["traceId"] == "" {
		t.Fatal("traceId is empty")
	}
}
