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
