package handlers

import (
	"net/http"

	"reader/internal/api/generated"
)

func (h *SourceHandler) GetHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, generated.HealthResponse{Status: generated.Ok})
}

func (h *SourceHandler) GetVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, generated.VersionResponse{Version: "0.0.1"})
}

func (h *SourceHandler) GetOpenAPI(w http.ResponseWriter, _ *http.Request) {
	spec, err := generated.GetSwagger()
	if err != nil {
		http.Error(w, "openapi spec unavailable", http.StatusInternalServerError)
		return
	}
	raw, err := spec.MarshalJSON()
	if err != nil {
		http.Error(w, "openapi spec unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}
