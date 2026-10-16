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
	"reader/internal/infra/ratelimit"
	ruleinfra "reader/internal/infra/rule"
	ruleregex "reader/internal/infra/rule/regex"
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
	limiter *ratelimit.Limiter
}

type SearchInput struct {
	Source  *source.BookSource
	Keyword string
	Page    int
}

func NewSingleSourceSearcher(fetcher fetch.Fetcher, scriptRuntime *script.Runtime) *SingleSourceSearcher {
	return &SingleSourceSearcher{fetcher: fetcher, script: scriptRuntime}
}

// WithLimiter 注入全局/单源并发与 concurrentRate 限流器；未注入时不限流。
func (s *SingleSourceSearcher) WithLimiter(limiter *ratelimit.Limiter) *SingleSourceSearcher {
	s.limiter = limiter
	return s
}

func (s *SingleSourceSearcher) Search(ctx context.Context, input SearchInput) ([]book.SearchBook, error) {
	ctx = script.WithStage(ctx, "search")
	src := input.Source
	if src == nil || strings.TrimSpace(src.SearchURL) == "" {
		return nil, ErrEmptySearchURL
	}
	req, err := s.buildSearchRequest(ctx, src, input.Keyword, input.Page)
	if err != nil {
		return nil, err
	}
	resp, err := ratelimit.Do(ctx, s.limiter, src.BookSourceURL, src.ConcurrentRate, func(ctx context.Context) (*fetch.Response, error) {
		return s.fetcher.Fetch(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	if s.script != nil {
		resp, err = sourceapp.NewScriptIntegration(s.script).LoginCheck(ctx, src, resp)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(src.BookURLPattern) != "" {
		matched, err := matchesBookURLPattern(src.BookURLPattern, resp.URL)
		if err != nil {
			return nil, err
		}
		if matched {
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
		Source:      s.script.SourceBindings(ctx, src),
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
			Source:      s.script.SourceBindings(ctx, src),
		})
		itemAnalyzer.SetVariables(baseVars)
		name, _ := itemAnalyzer.GetString(ctx, rule.Name, item)
		name = bookapp.FormatBookName(name)
		if name == "" {
			continue
		}
		author, _ := itemAnalyzer.GetString(ctx, rule.Author, item)
		bookURL, _ := itemAnalyzer.GetURL(ctx, rule.BookURL, item)
		if bookURL == "" {
			bookURL = resp.URL
		}
		if _, ok := seen[bookURL]; ok {
			continue
		}
		seen[bookURL] = struct{}{}
		kinds, _ := itemAnalyzer.GetStringList(ctx, rule.Kind, item)
		intro, _ := itemAnalyzer.GetString(ctx, rule.Intro, item)
		cover, _ := itemAnalyzer.GetURL(ctx, rule.CoverURL, item)
		wordCount, _ := itemAnalyzer.GetString(ctx, rule.WordCount, item)
		lastChapter, _ := itemAnalyzer.GetString(ctx, rule.LastChapter, item)
		searchBook.BookURL = bookURL
		searchBook.Name = name
		searchBook.Author = bookapp.FormatBookAuthor(author)
		searchBook.Kind = strings.TrimSpace(strings.Join(kinds, ","))
		searchBook.CoverURL = cover
		searchBook.Intro = bookapp.FormatIntro(intro)
		searchBook.WordCount = bookapp.FormatWordCount(wordCount)
		searchBook.LatestChapterTitle = strings.TrimSpace(lastChapter)
		if variable := itemAnalyzer.VariablesJSON(); variable != "" {
			searchBook.Variable = variable
		}
		results = append(results, searchBook)
	}
	if reverse {
		reverseSearchBooks(results)
	}
	if len(results) == 0 && strings.TrimSpace(src.BookURLPattern) == "" {
		// 对齐 Legado BookList.kt:100-106：仅在未配置 bookUrlPattern 时才按详情页回退解析。
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
	if err := sourceapp.ValidateSupportedURLOptions(custom.Attribute); err != nil {
		return fetch.Request{}, fmt.Errorf("%w: %v", ErrUnsupportedSearchURL, err)
	}
	targetURL := urlrule.Absolute(sourceRequestBase(src.BookSourceURL), custom.URL)
	if targetURL == "" {
		targetURL = custom.URL
	}
	req := fetch.Request{URL: targetURL, Method: "GET", Headers: map[string]string{}, UseCookieJar: src.EnabledCookieJar}
	if headers, err := parseSearchHeaders(ctx, runtime, src, keyword, page); err != nil {
		return fetch.Request{}, err
	} else {
		for key, value := range headers {
			req.Headers[key] = value
		}
	}
	applySearchURLOptions(&req, custom.Attribute)
	sourceapp.ApplyNetworkOptions(&req, custom.Attribute)
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
		"key":     keyword,
		"page":    page,
		"source":  src,
		"baseUrl": sourceRequestBase(src.BookSourceURL),
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
	// 对齐 Legado AnalyzeUrl.replaceKeyPageJs：内嵌表达式与页数规则替换后处理 <a,b,c> 分页列表。
	return urlrule.ReplacePagePlaceholders(out, page)
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

func parseSearchHeaders(ctx context.Context, runtime *script.Runtime, src *source.BookSource, keyword string, page int) (map[string]string, error) {
	raw := strings.TrimSpace(src.Header)
	if raw == "" || strings.HasPrefix(strings.ToLower(raw), "@js:") || strings.HasPrefix(strings.ToLower(raw), "<js>") {
		if raw == "" {
			return map[string]string{}, nil
		}
		if runtime == nil {
			return map[string]string{}, nil
		}
		scriptText := strings.TrimSpace(raw)
		if strings.HasPrefix(strings.ToLower(scriptText), "@js:") {
			scriptText = strings.TrimSpace(scriptText[4:])
		} else if strings.HasPrefix(strings.ToLower(scriptText), "<js>") && strings.HasSuffix(strings.ToLower(scriptText), "</js>") {
			scriptText = strings.TrimSpace(scriptText[4 : len(scriptText)-5])
		}
		value, err := runtime.EvalWithBindings(ctx, scriptText, searchURLBindings(src, keyword, page))
		if err != nil {
			return nil, err
		}
		return parseSearchHeaderValue(value)
	}
	headers := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &headers); err != nil {
		loose := urlrule.ParseAttributeObject(raw)
		if len(loose) == 0 {
			return nil, err
		}
		for key, value := range loose {
			headers[key] = fmt.Sprint(value)
		}
	}
	return headers, nil
}

func parseSearchHeaderValue(value any) (map[string]string, error) {
	var raw []byte
	switch typed := value.(type) {
	case string:
		raw = []byte(typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		raw = encoded
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
	}
	return headers, nil
}

func applySearchURLOptions(req *fetch.Request, opts map[string]any) {
	var (
		charsetName string
		rawFormBody string
	)
	for key, value := range opts {
		switch strings.ToLower(key) {
		case "method":
			if method, ok := value.(string); ok && strings.TrimSpace(method) != "" {
				req.Method = strings.ToUpper(strings.TrimSpace(method))
			}
		case "body":
			switch body := value.(type) {
			case string:
				rawFormBody = body
				req.Body = []byte(body)
			case map[string]any, []any:
				if raw, err := json.Marshal(body); err == nil {
					req.Body = raw
					if req.Headers["Content-Type"] == "" {
						req.Headers["Content-Type"] = "application/json"
					}
				}
			}
		case "headers":
			mergeSearchHeaders(req.Headers, value)
		case "origin":
			// 对齐 Legado 书源/正文路径：origin 同时作为 Origin 与缺省 Referer。
			if origin, ok := value.(string); ok && strings.TrimSpace(origin) != "" {
				req.Headers["Origin"] = strings.TrimSpace(origin)
				if req.Headers["Referer"] == "" {
					req.Headers["Referer"] = strings.TrimSpace(origin)
				}
			}
		case "type":
			// 对齐 Legado AnalyzeUrl：type 键存在且值非 null 时按 hex 二进制处理响应。
			if value != nil {
				req.EncodeBodyAsHex = true
			}
		case "charset":
			if text, ok := value.(string); ok {
				charsetName = strings.TrimSpace(text)
			}
		case "retry":
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
	}
	req.URL = urlrule.EncodeURLQuery(req.URL, charsetName)
	if rawFormBody != "" && looksLikeFormBody(rawFormBody) {
		// 对齐 Legado AnalyzeUrl:279-283：只有显式设置了非表单 Content-Type 时才保留原文，
		// 否则按表单编码一次（不再重复编码），并补上表单 Content-Type。
		if strings.TrimSpace(req.Headers["Content-Type"]) == "" {
			req.Body = []byte(urlrule.EncodeFormString(rawFormBody, charsetName))
			req.Headers["Content-Type"] = "application/x-www-form-urlencoded"
		} else {
			req.Body = []byte(rawFormBody)
		}
	}
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

// matchesBookURLPattern 对齐 Legado BookList.kt 的 baseUrl.matches(pattern)：整串匹配。
// Legado 在 pattern 非法时会抛异常，这里同样返回错误，避免静默按“不匹配”处理。
func matchesBookURLPattern(pattern string, value string) (bool, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false, nil
	}
	matched, err := ruleregex.MatchString("^(?:"+pattern+")$", value)
	if err != nil {
		return false, fmt.Errorf("bookUrlPattern %q is invalid: %w", pattern, err)
	}
	return matched, nil
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
			// 对齐 Legado AnalyzeUrl entry.value.toString()：非字符串值也统一转成字符串。
			headers[key] = fmt.Sprint(raw)
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
		"result":  input,
		"src":     s.src,
		"baseUrl": s.baseURL,
		"source":  s.source,
		"book":    s.book,
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
