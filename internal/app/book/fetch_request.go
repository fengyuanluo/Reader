package bookapp

import (
	"context"
	"strings"

	sourceapp "reader/internal/app/source"
	domainbook "reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/source"
	"reader/internal/infra/requestoption"
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
		Script:      sourceapp.ScriptAdapter{Runtime: scriptRuntime, BaseURL: baseURL, Source: src, Book: b},
	})
	if b != nil {
		expander.LoadVariables(b.Variable)
	}
	if expanded, err := expander.ExpandString(ctx, rawURL, map[string]any{}); err == nil && strings.TrimSpace(expanded) != "" {
		rawURL = expanded
	}
	req, err := buildBookFetchRequest(ctx, rawURL, src, b, scriptRuntime)
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

func buildBookFetchRequest(ctx context.Context, rawURL string, src *source.BookSource, b *domainbook.Book, scriptRuntime *script.Runtime) (fetch.Request, error) {
	headers, err := sourceapp.NewScriptIntegration(scriptRuntime).HeaderMap(ctx, src)
	if err != nil {
		return fetch.Request{}, err
	}
	req := fetch.Request{Method: "GET", Headers: headers, AllowNonSuccess: true}
	if src != nil {
		req.UseCookieJar = src.EnabledCookieJar
		req.SourceKey = src.BookSourceURL
		req.ConcurrentRate = src.ConcurrentRate
	}
	bindings := script.Bindings{"baseUrl": requestBaseURL(rawURL, src)}
	if b != nil {
		bindings["book"] = b
	}
	return requestoption.Build(ctx, rawURL, sourceURLForAnalyzer(src), req, sourceapp.RequestEvaluator(scriptRuntime, src, bindings))
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

func executeBookFetchRequest(ctx context.Context, fetcher fetch.Fetcher, req fetch.Request, src *source.BookSource, b *domainbook.Book, runtime *script.Runtime) (*fetch.Response, error) {
	bindings := script.Bindings{"baseUrl": req.URL}
	if b != nil {
		bindings["book"] = b
	}
	return requestoption.Fetch(ctx, fetcher, req, sourceapp.RequestEvaluator(runtime, src, bindings))
}
