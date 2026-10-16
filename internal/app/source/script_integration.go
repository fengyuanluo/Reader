package sourceapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"reader/internal/domain/fetch"
	domainsource "reader/internal/domain/source"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/script"
)

var ErrLoginCheckFailed = errors.New("login check failed")

type ScriptIntegration struct {
	runtime *script.Runtime
}

func NewScriptIntegration(runtime *script.Runtime) *ScriptIntegration {
	return &ScriptIntegration{runtime: runtime}
}

func (s *ScriptIntegration) HeaderMap(ctx context.Context, src *domainsource.BookSource) (map[string]string, error) {
	return s.HeaderMapWithBindings(ctx, src, nil)
}

func (s *ScriptIntegration) HeaderMapWithBindings(ctx context.Context, src *domainsource.BookSource, extra script.Bindings) (map[string]string, error) {
	ctx = script.WithSubStage(ctx, "header_js")
	header := strings.TrimSpace(src.Header)
	loginHeaders := map[string]string{}
	if src != nil {
		if raw, ok := s.runtime.SourceBindings(ctx, src)["getLoginHeader"].(func() string); ok {
			if parsed, err := parseHeaderJSON(raw()); err == nil {
				loginHeaders = parsed
			}
		}
	}
	if header == "" {
		return loginHeaders, nil
	}
	bindings := sourceScriptBindings(src)
	for key, value := range extra {
		bindings[key] = value
	}
	if strings.HasPrefix(strings.ToLower(header), "@js:") {
		if s.runtime == nil {
			return map[string]string{}, nil
		}
		value, err := s.runtime.EvalWithBindings(ctx, strings.TrimSpace(header[4:]), bindings)
		if err != nil {
			return nil, err
		}
		headers, err := parseHeaderJSON(value)
		if err != nil {
			return nil, err
		}
		for key, value := range loginHeaders {
			headers[key] = value
		}
		return headers, nil
	}
	if strings.HasPrefix(strings.ToLower(header), "<js>") && strings.HasSuffix(strings.ToLower(header), "</js>") {
		if s.runtime == nil {
			return map[string]string{}, nil
		}
		value, err := s.runtime.EvalWithBindings(ctx, strings.TrimSpace(header[4:len(header)-5]), bindings)
		if err != nil {
			return nil, err
		}
		headers, err := parseHeaderJSON(value)
		if err != nil {
			return nil, err
		}
		for key, value := range loginHeaders {
			headers[key] = value
		}
		return headers, nil
	}
	headers, err := parseHeaderJSON(header)
	if err != nil {
		return nil, err
	}
	for key, value := range loginHeaders {
		headers[key] = value
	}
	return headers, nil
}

func (s *ScriptIntegration) LoginCheck(ctx context.Context, src *domainsource.BookSource, resp *fetch.Response) (*fetch.Response, error) {
	ctx = script.WithSubStage(ctx, "login_check_js")
	checkJS := strings.TrimSpace(src.LoginCheckJS)
	if checkJS == "" {
		return resp, nil
	}
	value, err := s.runtime.EvalWithBindings(ctx, checkJS, script.Bindings{
		"source": src,
		"result": string(resp.Body),
		"response": map[string]any{
			"statusCode": resp.StatusCode,
			"body":       string(resp.Body),
			"url":        resp.URL,
		},
	})
	if err != nil {
		return nil, err
	}
	switch typed := value.(type) {
	case string:
		next := *resp
		next.Body = []byte(typed)
		return &next, nil
	case map[string]any:
		if message, ok := typed["error"].(string); ok && message != "" {
			return nil, errors.Join(ErrLoginCheckFailed, errors.New(message))
		}
		if body, ok := typed["body"].(string); ok {
			next := *resp
			next.Body = []byte(body)
			return &next, nil
		}
	}
	return resp, nil
}

func sourceScriptBindings(src *domainsource.BookSource) script.Bindings {
	return script.Bindings{
		"source":  src,
		"baseUrl": sourceBaseURL(src),
	}
}

func sourceBaseURL(src *domainsource.BookSource) string {
	if src == nil {
		return ""
	}
	base := strings.TrimSpace(src.BookSourceURL)
	if i := strings.Index(base, "##"); i >= 0 {
		base = base[:i]
	}
	if i := strings.IndexAny(base, "#＃"); i >= 0 {
		base = base[:i]
	}
	return base
}

func parseHeaderJSON(value any) (map[string]string, error) {
	var raw []byte
	switch typed := value.(type) {
	case string:
		raw = []byte(typed)
	default:
		var err error
		raw, err = json.Marshal(typed)
		if err != nil {
			return nil, err
		}
	}
	headers := map[string]string{}
	if err := json.Unmarshal(raw, &headers); err != nil {
		loose := urlrule.ParseAttributeObject(string(raw))
		if len(loose) == 0 {
			return nil, err
		}
		for key, value := range loose {
			headers[key] = fmt.Sprint(value)
		}
		return headers, nil
	}
	return headers, nil
}

var (
	// ErrLoginURLMissing 表示书源没有声明 loginUrl，无法执行登录（对齐 Legado BaseSource.getLoginJs 返回 null）。
	ErrLoginURLMissing = errors.New("source loginUrl is not configured")
	// ErrLoginRuntimeUnavailable 表示脚本运行时未装配，登录脚本无法执行。
	ErrLoginRuntimeUnavailable = errors.New("script runtime is not available")
	// ErrLoginInfoNotPersisted 表示 loginInfo 写入持久层失败。
	ErrLoginInfoNotPersisted = errors.New("persist login info failed")
	// ErrUnsupportedLoginFlow 表示该登录流程需要 Legado 的 WebView，本服务不支持无头浏览器。
	ErrUnsupportedLoginFlow = errors.New("webView login is not supported")
)

// LoginState 是书源持久化的登录状态；空串表示该条目不存在。
type LoginState struct {
	Info   string
	Header string
}

// LoginStateOf 读取持久化的 loginInfo/loginHeader（对齐 Legado BaseSource.getLoginInfo/getLoginHeader）。
func (s *ScriptIntegration) LoginStateOf(ctx context.Context, src *domainsource.BookSource) LoginState {
	if s == nil || s.runtime == nil || src == nil {
		return LoginState{}
	}
	bindings := s.runtime.SourceBindings(ctx, src)
	state := LoginState{}
	if get, ok := bindings["getLoginInfo"].(func() any); ok {
		if value, ok := get().(string); ok {
			state.Info = value
		}
	}
	if get, ok := bindings["getLoginHeader"].(func() string); ok {
		state.Header = get()
	}
	return state
}

// Login 对齐 Legado BaseSource.login/SourceLoginViewModel.login：先把表单值持久化为 loginInfo，
// 再执行 loginUrl 中的 login 函数；脚本通过 source.putLoginHeader 写入的登录头同样会持久化。
// values 为空时只清除 loginInfo（Legado 对空表单的处理），不执行脚本。
func (s *ScriptIntegration) Login(ctx context.Context, src *domainsource.BookSource, values map[string]string) error {
	if s == nil || s.runtime == nil {
		return ErrLoginRuntimeUnavailable
	}
	if src == nil {
		return ErrLoginURLMissing
	}
	bindings := s.runtime.SourceBindings(ctx, src)
	if len(values) == 0 {
		if remove, ok := bindings["removeLoginInfo"].(func() bool); ok {
			remove()
		}
		return nil
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	put, _ := bindings["putLoginInfo"].(func(string) bool)
	if put == nil || !put(string(raw)) {
		return ErrLoginInfoNotPersisted
	}
	scriptText, isScript := scriptRuleText(src.LoginURL)
	if !isScript {
		// Legado getLoginJs 对无前缀的 loginUrl 也按脚本处理。
		scriptText = strings.TrimSpace(src.LoginURL)
	}
	if scriptText == "" {
		return ErrLoginURLMissing
	}
	ctx = script.WithSubStage(ctx, "login")
	_, err = s.runtime.EvalWithBindings(ctx, loginScriptSource(scriptText), sourceScriptBindings(src))
	return err
}

// ClearLogin 清除持久化的 loginInfo 与 loginHeader（对齐 Legado removeLoginInfo/removeLoginHeader）。
func (s *ScriptIntegration) ClearLogin(ctx context.Context, src *domainsource.BookSource) {
	if s == nil || s.runtime == nil || src == nil {
		return
	}
	bindings := s.runtime.SourceBindings(ctx, src)
	if remove, ok := bindings["removeLoginInfo"].(func() bool); ok {
		remove()
	}
	if remove, ok := bindings["removeLoginHeader"].(func() bool); ok {
		remove()
	}
}

// loginScriptSource 包一层 login 函数调用，与 Legado BaseSource.login 的注入代码一致。
func loginScriptSource(scriptText string) string {
	return scriptText + `
if (typeof login == 'function') {
  login.apply(this);
} else {
  throw('Function login not implements!!!');
}
`
}

// scriptRuleText 提取 Legado "@js:"/"<js>" 规则里的脚本文本；非脚本规则返回 ok=false。
func scriptRuleText(raw string) (string, bool) {
	text := strings.TrimSpace(raw)
	lower := strings.ToLower(text)
	switch {
	case strings.HasPrefix(lower, "@js:"):
		return strings.TrimSpace(text[4:]), true
	case strings.HasPrefix(lower, "<js>") && strings.HasSuffix(lower, "</js>"):
		return strings.TrimSpace(text[4 : len(text)-5]), true
	default:
		return "", false
	}
}
