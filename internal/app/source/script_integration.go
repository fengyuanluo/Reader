package sourceapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"reader/internal/domain/fetch"
	domainsource "reader/internal/domain/source"
	"reader/internal/infra/requestoption"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/script"
)

var (
	ErrLoginCheckFailed       = errors.New("login check failed")
	ErrUnsupportedSourceProxy = requestoption.ErrUnsupportedSourceProxy
)

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
	header := strings.TrimSpace(src.Header)
	loginHeaders := map[string]string{}
	if src != nil {
		if raw, ok := script.SourceBindings(src)["getLoginHeader"].(func() string); ok {
			if parsed, err := parseHeaderJSON(raw()); err == nil {
				loginHeaders = parsed
			}
		}
	}
	if header == "" {
		return finalizeHeaderMap(loginHeaders)
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
		return finalizeHeaderMap(headers)
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
		return finalizeHeaderMap(headers)
	}
	headers, err := parseHeaderJSON(header)
	if err != nil {
		return nil, err
	}
	for key, value := range loginHeaders {
		headers[key] = value
	}
	return finalizeHeaderMap(headers)
}

func finalizeHeaderMap(headers map[string]string) (map[string]string, error) {
	if err := requestoption.SanitizeSourceHeaders(headers); err != nil {
		return nil, err
	}
	return headers, nil
}

func (s *ScriptIntegration) LoginCheck(ctx context.Context, src *domainsource.BookSource, resp *fetch.Response) (*fetch.Response, error) {
	checkJS := strings.TrimSpace(src.LoginCheckJS)
	if checkJS == "" {
		return resp, nil
	}
	value, err := s.runtime.EvalWithBindings(ctx, checkJS, script.Bindings{
		"source":     script.SourceBindings(src),
		"__source":   src,
		"baseUrl":    sourceBaseURL(src),
		"result":     resp,
		"__response": resp,
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
		"source":   script.SourceBindings(src),
		"__source": src,
		"baseUrl":  sourceBaseURL(src),
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
