package searchapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	bookapp "reader/internal/app/book"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	domainrule "reader/internal/domain/rule"
	"reader/internal/domain/source"
	"reader/internal/infra/requestoption"
	ruleinfra "reader/internal/infra/rule"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/script"
)

var (
	ErrEmptySearchURL       = errors.New("searchUrl is required")
	ErrUnsupportedSearchURL = errors.New("searchUrl uses unsupported Legado URL script")
	searchURLJSPattern      = regexp.MustCompile(`(?is)<js>([\w\W]*?)</js>|@js:([\w\W]*)`)
)

type SingleSourceSearcher struct {
	fetcher fetch.Fetcher
	script  *script.Runtime
}

type SearchInput struct {
	Source  *source.BookSource
	Keyword string
	Page    int
}

func NewSingleSourceSearcher(fetcher fetch.Fetcher, scriptRuntime *script.Runtime) *SingleSourceSearcher {
	return &SingleSourceSearcher{fetcher: fetcher, script: scriptRuntime}
}

func (s *SingleSourceSearcher) Search(ctx context.Context, input SearchInput) ([]book.SearchBook, error) {
	src := input.Source
	if src == nil || strings.TrimSpace(src.SearchURL) == "" {
		return nil, ErrEmptySearchURL
	}
	req, err := s.buildSearchRequest(ctx, src, input.Keyword, input.Page)
	if err != nil {
		return nil, err
	}
	evaluator := sourceapp.RequestEvaluator(s.script, src, script.Bindings{
		"key": input.Keyword, "page": input.Page, "baseUrl": req.URL,
	})
	resp, err := requestoption.Fetch(ctx, s.fetcher, req, evaluator)
	if err != nil {
		return nil, err
	}
	if s.script != nil {
		resp, err = sourceapp.NewScriptIntegration(s.script).LoginCheck(ctx, src, resp)
		if err != nil {
			return nil, err
		}
	}
	if src.BookURLPattern != "" {
		if ok, _ := regexp.MatchString(src.BookURLPattern, resp.URL); ok {
			preview, err := bookapp.PreviewSearchBookFromInfo(ctx, src, resp.URL, string(resp.Body), book.SearchBook{
				BookURL:     resp.URL,
				Origin:      src.BookSourceURL,
				OriginName:  src.BookSourceName,
				OriginOrder: src.CustomOrder,
				Type:        src.BookSourceType,
			}, s.script)
			if err == nil && strings.TrimSpace(preview.Name) != "" {
				return []book.SearchBook{preview}, nil
			}
			return []book.SearchBook{{
				BookURL:     resp.URL,
				Origin:      src.BookSourceURL,
				OriginName:  src.BookSourceName,
				OriginOrder: src.CustomOrder,
				Type:        src.BookSourceType,
			}}, nil
		}
	}
	rule := src.RuleSearch.Value
	if rule == nil || strings.TrimSpace(rule.BookList) == "" {
		return nil, errors.New("ruleSearch.bookList is required")
	}
	listRule, reverse := normalizeListRule(rule.BookList)
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		RedirectURL: resp.URL,
		SourceURL:   src.BookSourceURL,
		Script:      scriptAdapter{runtime: s.script, baseURL: resp.URL, source: src, src: string(resp.Body)},
		Source:      script.SourceBindings(src),
	})
	items, err := analyzer.GetElements(ctx, listRule, string(resp.Body))
	if err != nil {
		return nil, err
	}
	results := []book.SearchBook{}
	seen := map[string]struct{}{}
	baseVars := analyzer.Variables()
	for _, item := range items {
		searchBook := book.SearchBook{
			Origin:      src.BookSourceURL,
			OriginName:  src.BookSourceName,
			Type:        src.BookSourceType,
			OriginOrder: src.CustomOrder,
			Variable:    encodeSearchVariables(baseVars),
		}
		itemAnalyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
			RedirectURL: resp.URL,
			SourceURL:   src.BookSourceURL,
			Script:      scriptAdapter{runtime: s.script, baseURL: resp.URL, source: src, src: string(resp.Body), book: &searchBook},
			Book:        &searchBook,
			Source:      script.SourceBindings(src),
		})
		itemAnalyzer.SetVariables(baseVars)
		name, _ := itemAnalyzer.GetString(ctx, rule.Name, item)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		author, _ := itemAnalyzer.GetString(ctx, rule.Author, item)
		bookURL := firstNonEmpty(itemAnalyzer.GetStringList(ctx, rule.BookURL, item))
		if bookURL == "" {
			bookURL = resp.URL
		} else if absoluteBookURL := urlrule.Absolute(resp.URL, bookURL); absoluteBookURL != "" {
			bookURL = absoluteBookURL
		}
		if _, ok := seen[bookURL]; ok {
			continue
		}
		seen[bookURL] = struct{}{}
		kind, _ := itemAnalyzer.GetString(ctx, rule.Kind, item)
		intro, _ := itemAnalyzer.GetString(ctx, rule.Intro, item)
		cover := firstNonEmpty(itemAnalyzer.GetStringList(ctx, rule.CoverURL, item))
		wordCount, _ := itemAnalyzer.GetString(ctx, rule.WordCount, item)
		lastChapter, _ := itemAnalyzer.GetString(ctx, rule.LastChapter, item)
		searchBook.BookURL = bookURL
		searchBook.Name = name
		searchBook.Author = strings.TrimSpace(author)
		searchBook.Kind = strings.TrimSpace(kind)
		searchBook.CoverURL = cover
		searchBook.Intro = strings.TrimSpace(intro)
		searchBook.WordCount = strings.TrimSpace(wordCount)
		searchBook.LatestChapterTitle = strings.TrimSpace(lastChapter)
		if variable := itemAnalyzer.VariablesJSON(); variable != "" {
			searchBook.Variable = variable
		}
		results = append(results, searchBook)
	}
	if reverse {
		reverseSearchBooks(results)
	}
	if len(results) == 0 {
		preview, err := bookapp.PreviewSearchBookFromInfo(ctx, src, resp.URL, string(resp.Body), book.SearchBook{
			BookURL:     resp.URL,
			Origin:      src.BookSourceURL,
			OriginName:  src.BookSourceName,
			OriginOrder: src.CustomOrder,
			Type:        src.BookSourceType,
		}, s.script)
		if err == nil && strings.TrimSpace(preview.Name) != "" {
			return []book.SearchBook{preview}, nil
		}
	}
	return results, nil
}

func firstNonEmpty(values []string, err error) string {
	if err != nil {
		return ""
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func buildSearchURL(template string, keyword string, page int) string {
	req, err := buildSearchRequest(context.Background(), nil, &source.BookSource{SearchURL: template}, keyword, page)
	if err != nil {
		return buildSearchURLLegacy(template, keyword, page)
	}
	return req.URL
}

func (s *SingleSourceSearcher) buildSearchRequest(ctx context.Context, src *source.BookSource, keyword string, page int) (fetch.Request, error) {
	return buildSearchRequest(ctx, s.script, src, keyword, page)
}

func buildSearchRequest(ctx context.Context, runtime *script.Runtime, src *source.BookSource, keyword string, page int) (fetch.Request, error) {
	if src == nil || strings.TrimSpace(src.SearchURL) == "" {
		return fetch.Request{}, ErrEmptySearchURL
	}
	out, err := renderSearchURL(ctx, runtime, src, keyword, page)
	if err != nil {
		return fetch.Request{}, err
	}
	custom := urlrule.ParseCustom(out)
	if strings.Contains(custom.URL, "{{") || strings.Contains(custom.URL, "}}") {
		return fetch.Request{}, fmt.Errorf("%w: unresolved expression in %q", ErrUnsupportedSearchURL, src.SearchURL)
	}
	req := fetch.Request{
		Method:         "GET",
		Headers:        map[string]string{},
		UseCookieJar:   src.EnabledCookieJar,
		SourceKey:      src.BookSourceURL,
		ConcurrentRate: src.ConcurrentRate,
	}
	if headers, err := sourceapp.NewScriptIntegration(runtime).HeaderMapWithBindings(ctx, src, script.Bindings{"key": keyword, "page": page}); err != nil {
		return fetch.Request{}, err
	} else {
		for key, value := range headers {
			req.Headers[key] = value
		}
	}
	evaluator := sourceapp.RequestEvaluator(runtime, src, script.Bindings{
		"key": keyword, "page": page, "baseUrl": sourceRequestBase(src.BookSourceURL),
	})
	req, err = requestoption.Build(ctx, out, sourceRequestBase(src.BookSourceURL), req, evaluator)
	if err != nil {
		return fetch.Request{}, fmt.Errorf("%w: %v", ErrUnsupportedSearchURL, err)
	}
	return req, nil
}

func renderSearchURL(ctx context.Context, runtime *script.Runtime, src *source.BookSource, keyword string, page int) (string, error) {
	out := src.SearchURL
	if hasSearchURLJS(out) {
		if runtime == nil {
			return "", ErrUnsupportedSearchURL
		}
		next, err := applySearchURLScripts(ctx, runtime, src, keyword, page, out)
		if err != nil {
			return "", err
		}
		out = next
	}
	out = buildSearchURLPlaceholders(out, keyword, page, runtime == nil)
	out = strings.ReplaceAll(out, "{{source.key}}", strings.TrimRight(sourceRequestBase(src.BookSourceURL), "/"))
	out = strings.ReplaceAll(out, "{{source.bookSourceUrl}}", src.BookSourceURL)
	if strings.Contains(out, "{{") && strings.Contains(out, "}}") {
		if runtime == nil {
			return "", fmt.Errorf("%w: unresolved expression in %q", ErrUnsupportedSearchURL, src.SearchURL)
		}
		return replaceInlineSearchExpressions(ctx, runtime, src, keyword, page, out)
	}
	return out, nil
}

func hasSearchURLJS(raw string) bool {
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "@js:") || strings.Contains(lower, "<js>")
}

func applySearchURLScripts(ctx context.Context, runtime *script.Runtime, src *source.BookSource, keyword string, page int, raw string) (string, error) {
	matches := searchURLJSPattern.FindAllStringSubmatchIndex(raw, -1)
	if len(matches) == 0 {
		return raw, nil
	}
	bindings := searchURLBindings(src, keyword, page)
	start := 0
	result := raw
	for _, match := range matches {
		if match[0] > start {
			prefix := strings.TrimSpace(raw[start:match[0]])
			if prefix != "" {
				result = strings.ReplaceAll(prefix, "@result", result)
			}
		}
		var scriptText string
		switch {
		case match[2] >= 0 && match[3] >= 0:
			scriptText = raw[match[2]:match[3]]
		case match[4] >= 0 && match[5] >= 0:
			scriptText = raw[match[4]:match[5]]
		}
		bindings["result"] = result
		value, err := runtime.EvalWithBindings(ctx, strings.TrimSpace(scriptText), bindings)
		if err != nil {
			return "", err
		}
		result = scriptValueToString(value)
		start = match[1]
	}
	if len(raw) > start {
		tail := strings.TrimSpace(raw[start:])
		if tail != "" {
			result = strings.ReplaceAll(tail, "@result", result)
		}
	}
	return result, nil
}

func replaceInlineSearchExpressions(ctx context.Context, runtime *script.Runtime, src *source.BookSource, keyword string, page int, raw string) (string, error) {
	var b strings.Builder
	rest := raw
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			b.WriteString(rest)
			return b.String(), nil
		}
		b.WriteString(rest[:start])
		rest = rest[start+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			return "", fmt.Errorf("%w: unterminated inline expression", ErrUnsupportedSearchURL)
		}
		expr := strings.TrimSpace(rest[:end])
		value, err := runtime.EvalWithBindings(ctx, expr, searchURLBindings(src, keyword, page))
		if err != nil {
			return "", err
		}
		tail := rest[end+2:]
		b.WriteString(renderInlineSearchValue(value, b.String(), tail))
		rest = tail
	}
}

func scriptValueToString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func renderInlineSearchValue(value any, prefix string, suffix string) string {
	if value == nil {
		return ""
	}
	if !isLikelyJSONValuePosition(prefix, suffix) || !isInsideJSONObjectOrArray(prefix) {
		return scriptValueToString(value)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return scriptValueToString(value)
	}
	return string(raw)
}

func isLikelyJSONValuePosition(prefix string, suffix string) bool {
	prev := lastNonSpace(prefix)
	next := firstNonSpace(suffix)
	if prev == 0 || next == 0 {
		return false
	}
	if prev == '"' || prev == '\'' {
		return false
	}
	return (prev == ':' || prev == '[' || prev == ',') && (next == ',' || next == '}' || next == ']')
}

func isInsideJSONObjectOrArray(prefix string) bool {
	depth := 0
	inSingle := false
	inDouble := false
	escaped := false
	for _, r := range prefix {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case inSingle || inDouble:
			continue
		case r == '{' || r == '[':
			depth++
		case r == '}' || r == ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return depth > 0
}

func lastNonSpace(value string) byte {
	for i := len(value) - 1; i >= 0; i-- {
		if !isSpace(value[i]) {
			return value[i]
		}
	}
	return 0
}

func firstNonSpace(value string) byte {
	for i := 0; i < len(value); i++ {
		if !isSpace(value[i]) {
			return value[i]
		}
	}
	return 0
}

func isSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}

func searchURLBindings(src *source.BookSource, keyword string, page int) script.Bindings {
	return script.Bindings{
		"key":      keyword,
		"page":     page,
		"result":   nil,
		"source":   script.SourceBindings(src),
		"__source": src,
		"baseUrl":  sourceRequestBase(src.BookSourceURL),
	}
}

func buildSearchURLLegacy(template string, keyword string, page int) string {
	return buildSearchURLPlaceholders(template, keyword, page, true)
}

func buildSearchURLPlaceholders(template string, keyword string, page int, includeDoubleBraces bool) string {
	if page < 1 {
		page = 1
	}
	out := template
	if includeDoubleBraces {
		out = strings.ReplaceAll(out, "{{key}}", keyword)
		out = strings.ReplaceAll(out, "{{page}}", strconv.Itoa(page))
	}
	out = replaceSingleBraceToken(out, "key", keyword)
	out = replaceSingleBraceToken(out, "page", strconv.Itoa(page))
	return out
}

func replaceSingleBraceToken(input string, token string, value string) string {
	needle := "{" + token + "}"
	var b strings.Builder
	for i := 0; i < len(input); {
		idx := strings.Index(input[i:], needle)
		if idx < 0 {
			b.WriteString(input[i:])
			break
		}
		idx += i
		b.WriteString(input[i:idx])
		prevIsBrace := idx > 0 && input[idx-1] == '{'
		prevIsDollar := idx > 0 && input[idx-1] == '$'
		next := idx + len(needle)
		nextIsBrace := next < len(input) && input[next] == '}'
		if prevIsBrace || nextIsBrace || prevIsDollar {
			b.WriteString(needle)
		} else {
			b.WriteString(value)
		}
		i = next
	}
	return b.String()
}

func sourceRequestBase(bookSourceURL string) string {
	base := strings.TrimSpace(bookSourceURL)
	if i := strings.Index(base, "##"); i >= 0 {
		base = base[:i]
	}
	if i := strings.IndexAny(base, "#＃"); i >= 0 {
		base = base[:i]
	}
	return base
}

func normalizeListRule(rule string) (string, bool) {
	rule = strings.TrimSpace(rule)
	reverse := false
	if strings.HasPrefix(rule, "-") {
		reverse = true
		rule = strings.TrimSpace(rule[1:])
	} else if strings.HasPrefix(rule, "+") {
		rule = strings.TrimSpace(rule[1:])
	}
	return rule, reverse
}

func reverseSearchBooks(items []book.SearchBook) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func mergeSearchHeaders(headers map[string]string, value any) {
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

type scriptAdapter struct {
	runtime *script.Runtime
	baseURL string
	source  *source.BookSource
	src     any
	book    *book.SearchBook
}

func (s scriptAdapter) Eval(ctx context.Context, scriptText string, input any) (any, error) {
	if s.runtime == nil {
		return nil, ruleinfra.ErrScriptUnavailable
	}
	return s.runtime.EvalWithBindings(ctx, scriptText, script.Bindings{
		"result":   input,
		"src":      s.src,
		"baseUrl":  s.baseURL,
		"source":   script.SourceBindings(s.source),
		"__source": s.source,
		"book":     s.book,
	})
}

var _ domainrule.ScriptRuntime = scriptAdapter{}

func encodeSearchVariables(vars map[string]string) string {
	if len(vars) == 0 {
		return ""
	}
	raw, err := json.Marshal(vars)
	if err != nil {
		return ""
	}
	return string(raw)
}
