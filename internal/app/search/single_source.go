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
	"reader/internal/domain/source"
	"reader/internal/infra/ratelimit"
	"reader/internal/infra/requestoption"
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

// WithLimiter 注入全局与单源并发上限；按源 concurrentRate 的限速由 fetcher（fetch.Request.SourceKey/
// ConcurrentRate）负责，这里不再重复限速。未注入时不限制并发。
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
	evaluator := sourceapp.RequestEvaluator(s.script, src, script.Bindings{
		"key": input.Keyword, "page": input.Page, "baseUrl": req.URL,
	})
	resp, err := ratelimit.Do(ctx, s.limiter, src.BookSourceURL, func(ctx context.Context) (*fetch.Response, error) {
		return requestoption.Fetch(ctx, s.fetcher, req, evaluator)
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
		Script:      sourceapp.ScriptAdapter{Runtime: s.script, BaseURL: resp.URL, Source: src, Src: string(resp.Body)},
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
			Script:      sourceapp.ScriptAdapter{Runtime: s.script, BaseURL: resp.URL, Source: src, Src: string(resp.Body), Book: &searchBook},
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
	bindings := searchURLBindings(ctx, runtime, src, keyword, page)
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
		value, err := runtime.EvalWithBindings(ctx, expr, searchURLBindings(ctx, runtime, src, keyword, page))
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

func searchURLBindings(ctx context.Context, runtime *script.Runtime, src *source.BookSource, keyword string, page int) script.Bindings {
	bindings := script.Bindings{
		"key":      keyword,
		"page":     page,
		"result":   nil,
		"__source": src,
		"baseUrl":  sourceRequestBase(src.BookSourceURL),
	}
	if runtime != nil {
		bindings["source"] = runtime.SourceBindings(ctx, src)
	}
	return bindings
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
