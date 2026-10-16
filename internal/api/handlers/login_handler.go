package handlers

import (
	"errors"
	"net/http"

	"reader/internal/api/generated"
	"reader/internal/api/middleware"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/source"
	"reader/internal/infra/script"
)

// GetBookSourceLogin 返回书源持久化的登录状态（loginInfo/loginHeader）。
func (h *SourceHandler) GetBookSourceLogin(w http.ResponseWriter, r *http.Request, sourceID generated.SourceId) {
	if h.login == nil {
		writeNotConfigured(w, r, "login service is not configured")
		return
	}
	state, err := h.login.State(r.Context(), string(sourceID))
	if err != nil {
		writeLoginError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapLoginState(state))
}

// LoginBookSource 持久化登录表单值并执行 loginUrl 脚本；空 body 等价于只清除 loginInfo。
func (h *SourceHandler) LoginBookSource(w http.ResponseWriter, r *http.Request, sourceID generated.SourceId) {
	if h.login == nil {
		writeNotConfigured(w, r, "login service is not configured")
		return
	}
	var body generated.LoginBookSourceJSONRequestBody
	// 请求体可选，对齐 Legado 的“提交空表单即清除登录信息”。
	if r.ContentLength != 0 {
		if !h.decodeJSON(w, r, &body) {
			return
		}
	}
	values := map[string]string{}
	if body.Values != nil {
		values = *body.Values
	}
	state, err := h.login.Login(r.Context(), string(sourceID), values)
	if err != nil {
		writeLoginError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapLoginState(state))
}

// DeleteBookSourceLogin 清除书源持久化的 loginInfo 与 loginHeader。
func (h *SourceHandler) DeleteBookSourceLogin(w http.ResponseWriter, r *http.Request, sourceID generated.SourceId) {
	if h.login == nil {
		writeNotConfigured(w, r, "login service is not configured")
		return
	}
	if err := h.login.Clear(r.Context(), string(sourceID)); err != nil {
		writeLoginError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func mapLoginState(state sourceapp.LoginStateView) generated.BookSourceLoginState {
	dto := generated.BookSourceLoginState{
		SourceId:    state.SourceID,
		HasLoginUrl: state.HasLoginURL,
		HasLoginUi:  state.HasLoginUI,
		LoggedIn:    state.LoggedIn,
	}
	if state.LoginInfo != nil {
		info := state.LoginInfo
		dto.LoginInfo = &info
	}
	if state.LoginHeader != nil {
		header := state.LoginHeader
		dto.LoginHeader = &header
	}
	return dto
}

func writeLoginError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, source.ErrNotFound):
		middleware.WriteError(w, r, http.StatusNotFound, "source_not_found", "book source not found", nil)
	case errors.Is(err, sourceapp.ErrLoginURLMissing), errors.Is(err, sourceapp.ErrLoginInfoNotPersisted):
		writeValidationError(w, r, err.Error())
	case errors.Is(err, sourceapp.ErrUnsupportedLoginFlow):
		middleware.WriteError(w, r, http.StatusNotImplemented, "unsupported_login_flow", err.Error(), nil)
	case errors.Is(err, sourceapp.ErrLoginRuntimeUnavailable):
		middleware.WriteError(w, r, http.StatusNotImplemented, "not_configured", err.Error(), nil)
	case errors.Is(err, script.ErrScriptFailed), errors.Is(err, script.ErrScriptTimeout):
		middleware.WriteError(w, r, http.StatusBadGateway, "script_failed", err.Error(), nil)
	default:
		middleware.WriteError(w, r, http.StatusInternalServerError, "internal_error", "internal error", nil)
	}
}
