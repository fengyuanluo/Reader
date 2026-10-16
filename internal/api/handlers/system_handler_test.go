package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"reader/internal/api/generated"
)

func TestGetVersionReturnsCurrentRelease(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)

	new(SourceHandler).GetVersion(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var response generated.VersionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Version != "0.1.0" {
		t.Fatalf("version = %q, want 0.1.0", response.Version)
	}
}
