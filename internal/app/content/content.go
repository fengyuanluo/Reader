package contentapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"

	replaceapp "reader/internal/app/replace"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/source"
	ruleinfra "reader/internal/infra/rule"
	"reader/internal/infra/rule/regex"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/script"
)

var (
	ErrContentRuleMissing            = errors.New("ruleContent is required")
	ErrContentEmpty                  = errors.New("content_empty")
	ErrUnsupportedContentBrowserFlow = errors.New("content sourceRegex requires unsupported browser flow")
)

var (
	contentImgTagPattern    = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	contentImgSrcPattern    = regexp.MustCompile(`(?is)\b(?:data-src|src)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	contentImgDataPattern   = regexp.MustCompile(`(?is)\bdata-[^=\s>]*\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	contentBlockTagPattern  = regexp.MustCompile(`(?is)</?(?:div|p|br|hr|h\d|article|dd|dl)\b[^>]*>`)
	contentCommentPattern   = regexp.MustCompile(`(?is)<!--[\w\W]*?-->`)
	contentOtherHTMLPattern = regexp.MustCompile(`(?is)</?[a-zA-Z][^<>]*>`)
	contentLineBreakPattern = regexp.MustCompile(`\s*\n+\s*`)
	contentTrimStartPattern = regexp.MustCompile(`^\s+`)
	contentTrimEndPattern   = regexp.MustCompile(`\s+$`)
	contentNoPrintReplacer  = strings.NewReplacer("\u00A0", " ", "\u2002", " ", "\u2003", " ", "\u2009", "", "\u200C", "", "\u200D", "")
)

type Input struct {
	Source         *source.BookSource
	Book           book.Book
	Chapter        book.BookChapter
	NextChapterURL string
}

type Result struct {
	Content string
	Chapter book.BookChapter
}

type Service struct {
	chapters  book.ChapterRepository
	fetcher   fetch.Fetcher
	script    *script.Runtime
	processor *replaceapp.Processor
}

func NewService(chapters book.ChapterRepository, fetcher fetch.Fetcher, scriptRuntime *script.Runtime) *Service {
	return &Service{chapters: chapters, fetcher: fetcher, script: scriptRuntime}
}

func (s *Service) WithProcessor(processor *replaceapp.Processor) *Service {
	s.processor = processor
	return s
}

func (s *Service) GetContent(ctx context.Context, input Input) (Result, error) {
	src := input.Source
	if src == nil || src.RuleContent.Value == nil {
		return Result{}, ErrContentRuleMissing
	}
	if strings.TrimSpace(src.RuleContent.Value.SourceRegex) != "" {
		return Result{}, ErrUnsupportedContentBrowserFlow
	}
	bookValue := input.Book
	chapter := input.Chapter
	// Legado WebBook.getContentAwait: 一级目录（卷名）章节不抓取、不解析正文规则，直接返回 tag 附加信息。
	// 卷章节的 url 是 toc 阶段按 Legado 语义合成的 title+index 占位串，抓取只会得到错误页面。
	if chapter.IsVolume && strings.HasPrefix(chapter.URL, chapter.Title) {
		return Result{Content: chapter.Tag, Chapter: chapter}, nil
	}
	bookID := book.IDFromURL(bookValue.BookURL)
	if s.chapters != nil {
		if cached, ok, err := s.chapters.GetContent(ctx, bookID, chapter.Index); err != nil {
			return Result{}, err
		} else if ok {
			return Result{Content: cached, Chapter: chapter}, nil
		}
	}
	chapterURL := urlrule.Absolute(chapter.BaseURL, chapter.URL)
	if chapterURL == "" {
		chapterURL = chapter.AbsoluteURL()
	}
	if chapterURL == bookValue.BookURL && strings.TrimSpace(bookValue.TocHTML) != "" {
		content, err := s.parseAllPages(ctx, src, &bookValue, &chapter, chapterURL, chapterURL, bookValue.TocHTML, input.NextChapterURL)
		if err != nil {
			return Result{}, err
		}
		if !chapter.IsVolume && strings.TrimSpace(content) == "" {
			return Result{}, ErrContentEmpty
		}
		if s.processor != nil {
			processed, err := s.processor.Process(ctx, replaceapp.ProcessInput{Book: bookValue, Chapter: chapter, Content: content})
			if err != nil {
				return Result{}, err
			}
			content = processed.Content
		}
		if s.chapters != nil {
			if err := s.chapters.SaveContent(ctx, bookID, chapter.Index, content); err != nil {
				return Result{}, err
			}
		}
		return Result{Content: content, Chapter: chapter}, nil
	}
	expander := ruleinfra.NewAnalyzer(ruleinfra.Context{
		BaseURL:     chapter.BaseURL,
		RedirectURL: chapter.BaseURL,
		SourceURL:   src.BookSourceURL,
		Script:      scriptAdapter{runtime: s.script, baseURL: chapter.BaseURL, source: src, book: &bookValue, chapter: &chapter},
		Chapter:     &chapter,
		Book:        &bookValue,
		Source:      script.SourceBindings(src),
	})
	expander.LoadVariables(bookValue.Variable)
	expander.LoadVariables(chapter.Variable)
	if expanded, err := expander.ExpandString(ctx, chapterURL, map[string]any{}); err == nil && strings.TrimSpace(expanded) != "" {
		chapterURL = expanded
	}
	req, err := buildContentFetchRequest(ctx, chapterURL, src, s.script)
	if err != nil {
		return Result{}, err
	}
	if len(req.Body) > 0 {
		if expandedBody, err := expander.ExpandString(ctx, string(req.Body), map[string]any{}); err == nil && strings.TrimSpace(expandedBody) != "" {
			req.Body = []byte(expandedBody)
		}
	}
	resp, err := s.fetcher.Fetch(ctx, req)
	if err != nil {
		return Result{}, err
	}
	if s.script != nil {
		resp, err = sourceapp.NewScriptIntegration(s.script).LoginCheck(ctx, src, resp)
		if err != nil {
			return Result{}, err
		}
		resp, err = s.applyContentWebJS(ctx, src, &bookValue, &chapter, resp, input.NextChapterURL)
		if err != nil {
			return Result{}, err
		}
	}
	content, err := s.parseAllPages(ctx, src, &bookValue, &chapter, req.URL, resp.URL, string(resp.Body), input.NextChapterURL)
	if err != nil {
		return Result{}, err
	}
	if !chapter.IsVolume && strings.TrimSpace(content) == "" {
		return Result{}, ErrContentEmpty
	}
	if s.processor != nil {
		processed, err := s.processor.Process(ctx, replaceapp.ProcessInput{Book: bookValue, Chapter: chapter, Content: content})
		if err != nil {
			return Result{}, err
		}
		content = processed.Content
	}
	if s.chapters != nil {
		if err := s.chapters.SaveContent(ctx, bookID, chapter.Index, content); err != nil {
			return Result{}, err
		}
	}
	return Result{Content: content, Chapter: chapter}, nil
}

func (s *Service) parseAllPages(ctx context.Context, src *source.BookSource, b *book.Book, chapter *book.BookChapter, baseURL string, redirectURL string, body string, nextChapterURL string) (string, error) {
	content, nextURLs, err := s.parsePage(ctx, src, b, chapter, baseURL, redirectURL, body, true, nextChapterURL)
	if err != nil {
		return "", err
	}
	pages := []string{content}
	seen := map[string]struct{}{redirectURL: {}}
	for len(nextURLs) == 1 {
		next := strings.TrimSpace(nextURLs[0])
		if next == "" {
			break
		}
		next = urlrule.Absolute(redirectURL, next)
		if _, ok := seen[next]; ok {
			break
		}
		seen[next] = struct{}{}
		req, err := buildContentFetchRequest(ctx, next, src, s.script)
		if err != nil {
			return "", err
		}
		resp, err := s.fetcher.Fetch(ctx, req)
		if err != nil {
			return "", err
		}
		if s.script != nil {
			resp, err = sourceapp.NewScriptIntegration(s.script).LoginCheck(ctx, src, resp)
			if err != nil {
				return "", err
			}
			resp, err = s.applyContentWebJS(ctx, src, b, chapter, resp, nextChapterURL)
			if err != nil {
				return "", err
			}
		}
		pageContent, pageNext, err := s.parsePage(ctx, src, b, chapter, next, resp.URL, string(resp.Body), true, nextChapterURL)
		if err != nil {
			return "", err
		}
		pages = append(pages, pageContent)
		nextURLs = pageNext
	}
	if len(nextURLs) > 1 {
		for _, next := range nextURLs {
			next = strings.TrimSpace(urlrule.Absolute(redirectURL, next))
			if next == "" {
				continue
			}
			if _, ok := seen[next]; ok {
				continue
			}
			req, err := buildContentFetchRequest(ctx, next, src, s.script)
			if err != nil {
				return "", err
			}
			resp, err := s.fetcher.Fetch(ctx, req)
			if err != nil {
				return "", err
			}
			if s.script != nil {
				resp, err = sourceapp.NewScriptIntegration(s.script).LoginCheck(ctx, src, resp)
				if err != nil {
					return "", err
				}
				resp, err = s.applyContentWebJS(ctx, src, b, chapter, resp, nextChapterURL)
				if err != nil {
					return "", err
				}
			}
			pageContent, _, err := s.parsePage(ctx, src, b, chapter, next, resp.URL, string(resp.Body), false, nextChapterURL)
			if err != nil {
				return "", err
			}
			pages = append(pages, pageContent)
		}
	}
	content = strings.Join(pages, "\n")
	if strings.TrimSpace(src.RuleContent.Value.ReplaceRegex) != "" {
		replaced, err := applyContentReplace(ctx, redirectURL, src.RuleContent.Value.ReplaceRegex, content, s.script)
		if err != nil {
			return "", err
		}
		content = strings.TrimSpace(replaced)
	}
	return content, nil
}

func (s *Service) applyContentWebJS(ctx context.Context, src *source.BookSource, b *book.Book, chapter *book.BookChapter, resp *fetch.Response, nextChapterURL string) (*fetch.Response, error) {
	if s.script == nil || src == nil || src.RuleContent.Value == nil {
		return resp, nil
	}
	scriptText := strings.TrimSpace(src.RuleContent.Value.WebJS)
	if scriptText == "" {
		return resp, nil
	}
	value, err := s.script.EvalWithBindings(ctx, scriptText, script.Bindings{
		"result":         string(resp.Body),
		"src":            string(resp.Body),
		"baseUrl":        resp.URL,
		"source":         script.SourceBindings(src),
		"__source":       src,
		"book":           b,
		"chapter":        chapter,
		"nextChapterUrl": nextChapterURL,
		"response": map[string]any{
			"statusCode": resp.StatusCode,
			"body":       string(resp.Body),
			"url":        resp.URL,
		},
	})
	if err != nil {
		return nil, err
	}
	body, ok := value.(string)
	if !ok {
		body = fmt.Sprint(value)
	}
	next := *resp
	next.Body = []byte(body)
	return &next, nil
}

func buildContentFetchRequest(ctx context.Context, rawURL string, src *source.BookSource, scriptRuntime *script.Runtime) (fetch.Request, error) {
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
			mergeContentHeaders(req.Headers, value)
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

func mergeContentHeaders(headers map[string]string, value any) {
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

func applyContentReplace(ctx context.Context, redirectURL string, rule string, content string, scriptRuntime *script.Runtime) (string, error) {
	parts := strings.Split(rule, "##")
	if len(parts) >= 2 && strings.TrimSpace(parts[0]) == "" {
		pattern := parts[1]
		replacement := ""
		if len(parts) >= 3 {
			replacement = parts[2]
		}
		return regex.Replace(content, pattern, replacement, len(parts) >= 4)
	}
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{RedirectURL: redirectURL, Script: scriptAdapter{runtime: scriptRuntime, baseURL: redirectURL}})
	return analyzer.GetString(ctx, rule, content)
}

func (s *Service) parsePage(ctx context.Context, src *source.BookSource, b *book.Book, chapter *book.BookChapter, baseURL string, redirectURL string, body string, getNext bool, nextChapterURL string) (string, []string, error) {
	contentRule := src.RuleContent.Value
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		BaseURL:     baseURL,
		RedirectURL: redirectURL,
		SourceURL:   src.BookSourceURL,
		Script:      scriptAdapter{runtime: s.script, baseURL: baseURL, source: src, book: b, chapter: chapter, src: body, nextChapterURL: nextChapterURL},
		Chapter:     chapter,
		Book:        b,
		Source:      script.SourceBindings(src),
	})
	analyzer.LoadVariables(b.Variable)
	analyzer.LoadVariables(chapter.Variable)
	if strings.TrimSpace(contentRule.Title) != "" {
		if title, err := analyzer.GetString(ctx, contentRule.Title, body); err == nil && strings.TrimSpace(title) != "" {
			chapter.Title = strings.TrimSpace(title)
		}
	}
	content, err := analyzer.GetString(ctx, contentRule.Content, body)
	if err != nil {
		return "", nil, err
	}
	content = normalizeContent(content, redirectURL)
	nextURLs := []string{}
	if getNext && strings.TrimSpace(contentRule.NextContentURL) != "" {
		values, err := analyzer.GetStringList(ctx, contentRule.NextContentURL, body)
		if err != nil {
			return "", nil, err
		}
		for _, value := range values {
			value = strings.TrimSpace(urlrule.Absolute(redirectURL, value))
			if value != "" {
				nextURLs = append(nextURLs, value)
			}
		}
	}
	return content, nextURLs, nil
}

func normalizeContent(content string, redirectURL string) string {
	content = normalizeContentHTML(content, redirectURL)
	content = html.UnescapeString(content)
	content = contentNoPrintReplacer.Replace(content)
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func normalizeContentHTML(content string, redirectURL string) string {
	if strings.TrimSpace(content) == "" || !strings.Contains(content, "<") {
		return content
	}

	images := make([]string, 0)
	content = contentImgTagPattern.ReplaceAllStringFunc(content, func(tag string) string {
		src := extractContentImageSource(tag)
		if strings.TrimSpace(src) == "" {
			return ""
		}
		normalized := urlrule.Absolute(redirectURL, src)
		if normalized == "" {
			normalized = strings.TrimSpace(src)
		}
		images = append(images, `<img src="`+html.EscapeString(normalized)+`">`)
		return fmt.Sprintf("__READER_IMG_%d__", len(images)-1)
	})
	content = contentCommentPattern.ReplaceAllString(content, "")
	content = contentBlockTagPattern.ReplaceAllString(content, "\n")
	content = contentOtherHTMLPattern.ReplaceAllString(content, "")
	content = contentTrimStartPattern.ReplaceAllString(content, "")
	content = contentTrimEndPattern.ReplaceAllString(content, "")
	content = contentLineBreakPattern.ReplaceAllString(content, "\n")
	for i, image := range images {
		content = strings.ReplaceAll(content, fmt.Sprintf("__READER_IMG_%d__", i), image)
	}
	return content
}

func extractContentImageSource(tag string) string {
	if match := contentImgSrcPattern.FindStringSubmatch(tag); len(match) == 3 {
		return strings.TrimSpace(firstNonEmptyCapture(match[1], match[2]))
	}
	if match := contentImgDataPattern.FindStringSubmatch(tag); len(match) == 3 {
		return strings.TrimSpace(firstNonEmptyCapture(match[1], match[2]))
	}
	return ""
}

func firstNonEmptyCapture(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type scriptAdapter struct {
	runtime        *script.Runtime
	baseURL        string
	source         *source.BookSource
	book           *book.Book
	chapter        *book.BookChapter
	src            any
	nextChapterURL string
}

func (s scriptAdapter) Eval(ctx context.Context, scriptText string, input any) (any, error) {
	if s.runtime == nil {
		return nil, ruleinfra.ErrScriptUnavailable
	}
	return s.runtime.EvalWithBindings(ctx, scriptText, script.Bindings{
		"result":         input,
		"src":            s.src,
		"baseUrl":        s.baseURL,
		"source":         script.SourceBindings(s.source),
		"__source":       s.source,
		"book":           s.book,
		"chapter":        s.chapter,
		"nextChapterUrl": s.nextChapterURL,
	})
}
