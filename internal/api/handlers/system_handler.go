package handlers

import (
	"net/http"

	apispec "reader/api"
	"reader/internal/api/generated"
)

// version 是 API 版本，可用 -ldflags "-X reader/internal/api/handlers.version=..." 在构建时注入，
// 使 /version 与 git tag/发布流程一致，避免三处手工同步（S18-02）。
var version = "0.1.0"

func (h *SourceHandler) GetHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, generated.HealthResponse{Status: generated.Ok})
}

func (h *SourceHandler) GetVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, generated.VersionResponse{Version: version})
}

func (h *SourceHandler) GetOpenAPI(w http.ResponseWriter, _ *http.Request) {
	raw, err := apispec.Spec.ReadFile(apispec.OpenAPIYAML)
	if err != nil {
		http.Error(w, "openapi spec unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}
