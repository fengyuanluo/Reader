package bookapp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	sourceapp "reader/internal/app/source"
	domainbook "reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/source"
	ruleinfra "reader/internal/infra/rule"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/script"
)

func prepareBookFetchRequest(ctx context.Context, rawURL string, src *source.BookSource, b *domainbook.Book, scriptRuntime *script.Runtime) (fetch.Request, error) {
	baseURL := requestBaseURL(rawURL, src)
	expander := ruleinfra.NewAnalyzer(ruleinfra.Context{
		BaseURL:     baseURL,
		RedirectURL: baseURL,
		SourceURL:   sourceURLForAnalyzer(src),
		Script:      scriptAdapter{runtime: scriptRuntime, baseURL: baseURL, source: src, book: b},
	})
	if b != nil {
		expander.LoadVariables(b.Variable)
	}
	if expanded, err := expander.ExpandString(ctx, rawURL, map[string]any{}); err == nil && strings.TrimSpace(expanded) != "" {
		rawURL = expanded
	}
	req, err := buildBookFetchRequest(ctx, rawURL, src, scriptRuntime)
	if err != nil {
		return fetch.Request{}, err
	}
	if len(req.Body) > 0 {
		if expandedBody, err := expander.ExpandString(ctx, string(req.Body), map[string]any{}); err == nil && strings.TrimSpace(expandedBody) != "" {
			req.Body = []byte(expandedBody)
		}
	}
	return req, nil
}

func buildBookFetchRequest(ctx context.Context, rawURL string, src *source.BookSource, scriptRuntime *script.Runtime) (fetch.Request, error) {
	custom := urlrule.ParseCustom(rawURL)
	if err := sourceapp.ValidateSupportedURLOptions(custom.Attribute, "method", "body", "headers", "type", "charset", "origin", "retry"); err != nil {
		return fetch.Request{}, err
	}
	req := fetch.Request{URL: custom.URL, Method: "GET", Headers: map[string]string{}, AllowNonSuccess: true, UseCookieJar: src != nil && src.EnabledCookieJar}
	headers, err := sourceapp.NewScriptIntegration(scriptRuntime).HeaderMap(ctx, src)
	if err != nil {
		return fetch.Request{}, err
	}
	for key, value := range headers {
		req.Headers[key] = value
	}
	var (
		charsetName string
		rawFormBody string
	)
	for key, value := range custom.Attribute {
		switch strings.ToLower(key) {
		case "method":
			if method, ok := value.(string); ok && strings.TrimSpace(method) != "" {
				req.Method = strings.ToUpper(strings.TrimSpace(method))
			}
		case "body":
			switch body := value.(type) {
			case string:
				rawFormBody = body
				if looksLikeFormBody(body) {
					req.Body = []byte(urlrule.EncodeFormString(body, ""))
				} else {
					req.Body = []byte(body)
				}
				if req.Headers["Content-Type"] == "" && looksLikeFormBody(body) {
					req.Headers["Content-Type"] = "application/x-www-form-urlencoded"
				}
			case map[string]any, []any:
				if encoded, err := json.Marshal(body); err == nil {
					req.Body = encoded
					if req.Headers["Content-Type"] == "" {
						req.Headers["Content-Type"] = "application/json"
					}
				}
			}
		case "headers":
			mergeBookHeaders(req.Headers, value)
		case "type":
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				req.EncodeBodyAsHex = true
			}
		case "origin":
			if origin, ok := value.(string); ok && strings.TrimSpace(origin) != "" {
				req.Headers["Origin"] = strings.TrimSpace(origin)
				if req.Headers["Referer"] == "" {
					req.Headers["Referer"] = strings.TrimSpace(origin)
				}
			}
		case "charset":
			if text, ok := value.(string); ok {
				charsetName = strings.TrimSpace(text)
			}
		case "retry":
			applyRetryOption(&req, value)
		}
	}
	req.URL = urlrule.EncodeURLQuery(req.URL, charsetName)
	if rawFormBody != "" && looksLikeFormBody(rawFormBody) {
		req.Body = []byte(urlrule.EncodeFormString(rawFormBody, charsetName))
	}
	return req, nil
}

func applyRetryOption(req *fetch.Request, value any) {
	switch typed := value.(type) {
	case float64:
		if typed > 0 {
			req.Retry = int(typed)
		}
	case int:
		if typed > 0 {
			req.Retry = typed
		}
	case int64:
		if typed > 0 {
			req.Retry = int(typed)
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil && parsed > 0 {
			req.Retry = parsed
		}
	}
}

func requestBaseURL(rawURL string, src *source.BookSource) string {
	custom := urlrule.ParseCustom(rawURL)
	baseURL := strings.TrimSpace(custom.URL)
	if src != nil {
		baseURL = strings.TrimSpace(urlrule.Absolute(sourceURLForAnalyzer(src), baseURL))
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(rawURL)
	}
	return baseURL
}

func sourceURLForAnalyzer(src *source.BookSource) string {
	if src == nil {
		return ""
	}
	return strings.TrimSpace(src.BookSourceURL)
}

func looksLikeFormBody(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}
	if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") || strings.HasPrefix(body, "<") {
		return false
	}
	return strings.Contains(body, "=") || strings.Contains(body, "&")
}

func mergeBookHeaders(headers map[string]string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, raw := range typed {
			if text, ok := raw.(string); ok {
				headers[key] = text
			}
		}
	case string:
		nested := map[string]string{}
		if err := json.Unmarshal([]byte(typed), &nested); err == nil {
			for key, text := range nested {
				headers[key] = text
			}
			return
		}
		for key, raw := range urlrule.ParseAttributeObject(typed) {
			headers[key] = fmt.Sprint(raw)
		}
	}
}
