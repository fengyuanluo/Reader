package contentapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"

	replaceapp "reader/internal/app/replace"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/source"
	"reader/internal/infra/concurrency"
	ruleinfra "reader/internal/infra/rule"
	"reader/internal/infra/rule/regex"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/script"
)

// pageConcurrency 是正文多分页并发抓取的 worker 上限；Legado 用 AppConfig.threadCount（默认 16），
// Reader 保守取 8，避免同时打满单个书源站点。
const pageConcurrency = 8

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
	// SkipCache 为真时不写入正文缓存，对应 Legado WebBook.getContent(needSave=false)：
	// 调试等一次性抓取不应污染真实阅读缓存。
	SkipCache bool
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
	ctx = script.WithStage(ctx, "content")
	src := input.Source
	if src == nil || src.RuleContent.Value == nil {
		return Result{}, ErrContentRuleMissing
	}
	// contentRule.sourceRegex 在 Legado 中只由 BackstageWebView 通过 WebViewClient.onLoadResource
	// 嗅探页面加载的资源 URL（BackstageWebView.kt:283-296）并把命中的 URL 当作响应体返回，
	// 没有浏览器拿不到这些资源请求；本项目明确不引入 webView，因此统一返回“不支持”而不是静默忽略规则。
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
	originalTitle := chapter.Title
	if s.chapters != nil {
		if cached, ok, err := s.chapters.GetContent(ctx, bookID, chapter.Index); err != nil {
			return Result{}, err
		} else if ok {
			// 缓存保存的是净化前正文，用户替换规则在读取时应用（对齐 Legado BookContent.kt:150-153）。
			content, err := s.ProcessContent(ctx, bookValue, chapter, cached)
			if err != nil {
				return Result{}, err
			}
			return Result{Content: content, Chapter: chapter}, nil
		}
	}
	chapterURL := urlrule.Absolute(chapter.BaseURL, chapter.URL)
	if chapterURL == "" {
		chapterURL = chapter.AbsoluteURL()
	}
	var content string
	if chapterURL == bookValue.BookURL && strings.TrimSpace(bookValue.TocHTML) != "" {
		parsed, err := s.parseAllPages(ctx, src, &bookValue, &chapter, chapterURL, chapterURL, bookValue.TocHTML, input.NextChapterURL)
		if err != nil {
			return Result{}, err
		}
		content = parsed
	} else {
		expander := ruleinfra.NewAnalyzer(ruleinfra.Context{
			BaseURL:     chapter.BaseURL,
			RedirectURL: chapter.BaseURL,
			SourceURL:   src.BookSourceURL,
			Script:      scriptAdapter{runtime: s.script, baseURL: chapter.BaseURL, source: src, book: &bookValue, chapter: &chapter},
			Chapter:     &chapter,
			Book:        &bookValue,
			Source:      s.script.SourceBindings(ctx, src),
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
		parsed, err := s.parseAllPages(ctx, src, &bookValue, &chapter, req.URL, resp.URL, string(resp.Body), input.NextChapterURL)
		if err != nil {
			return Result{}, err
		}
		content = parsed
	}
	if !chapter.IsVolume && strings.TrimSpace(content) == "" {
		return Result{}, ErrContentEmpty
	}
	// 正文规则解析出的标题写回章节表（对齐 Legado BookContent.kt:66-69），否则重启后会退回目录标题。
	if s.chapters != nil && !input.SkipCache {
		if chapter.Title != originalTitle {
			if err := s.chapters.SaveTitle(ctx, bookID, chapter.Index, chapter.Title); err != nil {
				return Result{}, err
			}
		}
		if err := s.chapters.SaveContent(ctx, bookID, chapter.Index, content); err != nil {
			return Result{}, err
		}
	}
	processed, err := s.ProcessContent(ctx, bookValue, chapter, content)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: processed, Chapter: chapter}, nil
}

// ProcessContent 对净化前正文应用用户替换规则、重新分段与简繁转换（对齐 Legado ContentProcessor.getContent：
// 缓存只保存净化前正文，用户规则在每次读取时应用，用户改动规则后已抓章节立即生效）。
func (s *Service) ProcessContent(ctx context.Context, b book.Book, chapter book.BookChapter, content string) (string, error) {
	if s.processor == nil {
		return content, nil
	}
	processed, err := s.processor.Process(ctx, replaceapp.ProcessInput{Book: b, Chapter: chapter, Content: content})
	if err != nil {
		return "", err
	}
	return processed.Content, nil
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
		// 对齐 Legado BookContent.kt:86-89：本章末页常带“下一章”链接，下一页 URL 等于下一章 URL 时必须停止分页，
		// 否则会按本章正文规则解析下一章页面，把下一章正文并入本章并写入缓存。
		if nextChapterURL != "" && next == urlrule.Absolute(redirectURL, nextChapterURL) {
			break
		}
		seen[next] = struct{}{}
		resp, err := s.fetchPage(ctx, src, b, chapter, next, nextChapterURL)
		if err != nil {
			return "", err
		}
		pageContent, pageNext, err := s.parsePage(ctx, src, b, chapter, next, resp.URL, string(resp.Body), true, nextChapterURL)
		if err != nil {
			return "", err
		}
		pages = append(pages, pageContent)
		nextURLs = pageNext
	}
	if len(nextURLs) > 1 {
		targets := make([]string, 0, len(nextURLs))
		for _, next := range nextURLs {
			next = strings.TrimSpace(urlrule.Absolute(redirectURL, next))
			if next == "" {
				continue
			}
			if _, ok := seen[next]; ok {
				continue
			}
			seen[next] = struct{}{}
			targets = append(targets, next)
		}
		// 对齐 Legado BookContent.kt:103-130：多页并发抓取，结果按规则返回顺序拼接。
		// 每个分页用 book/chapter 快照，避免并发解析时脚本写变量造成数据竞争。
		pageContents, err := concurrency.Map(ctx, pageConcurrency, targets, func(ctx context.Context, _ int, target string) (string, error) {
			pageBook := *b
			pageChapter := *chapter
			resp, err := s.fetchPage(ctx, src, &pageBook, &pageChapter, target, nextChapterURL)
			if err != nil {
				return "", err
			}
			pageContent, _, err := s.parsePage(ctx, src, &pageBook, &pageChapter, target, resp.URL, string(resp.Body), false, nextChapterURL)
			if err != nil {
				return "", err
			}
			return pageContent, nil
		})
		if err != nil {
			return "", err
		}
		pages = append(pages, pageContents...)
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

// fetchPage 抓取单个分页 URL，并按 Legado BookContent.kt 对每一页依次应用登录检查与正文 WebJS。
func (s *Service) fetchPage(ctx context.Context, src *source.BookSource, b *book.Book, chapter *book.BookChapter, url string, nextChapterURL string) (*fetch.Response, error) {
	req, err := buildContentFetchRequest(ctx, url, src, s.script)
	if err != nil {
		return nil, err
	}
	resp, err := s.fetcher.Fetch(ctx, req)
	if err != nil {
		return nil, err
	}
	if s.script == nil {
		return resp, nil
	}
	resp, err = sourceapp.NewScriptIntegration(s.script).LoginCheck(ctx, src, resp)
	if err != nil {
		return nil, err
	}
	return s.applyContentWebJS(ctx, src, b, chapter, resp, nextChapterURL)
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
		"source":         src,
		"book":           b,
		"chapter":        chapter,
		"title":          script.ChapterTitleBinding(chapter),
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
	if err := sourceapp.ValidateSupportedURLOptions(custom.Attribute); err != nil {
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
				// 表单编码推迟到 charset 与全部 URL 选项（含 headers）解析完成后统一做一次（见下方）。
				if sourceapp.LooksLikeFormBody(body) {
					rawFormBody = body
				} else {
					req.Body = []byte(body)
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
			sourceapp.ApplyRetryOption(&req, value)
		}
	}
	req.URL = urlrule.EncodeURLQuery(req.URL, charsetName)
	// 对齐 Legado AnalyzeUrl.kt:262-283：body 不是 JSON/XML 且未显式设置 Content-Type 时才按表单编码，
	// 且只编码一次（旧实现先按空 charset 编码、末尾再按 charset 编码，正文被 percent 编码两次导致服务端解析失败）。
	if rawFormBody != "" {
		if req.Headers["Content-Type"] == "" {
			req.Body = []byte(urlrule.EncodeFormString(rawFormBody, charsetName))
			req.Headers["Content-Type"] = "application/x-www-form-urlencoded"
		} else {
			req.Body = []byte(rawFormBody)
		}
	}
	sourceapp.ApplyNetworkOptions(&req, custom.Attribute)
	return req, nil
}

func mergeContentHeaders(headers map[string]string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		// 对齐 Legado AnalyzeUrl.kt:235-237 entry.value.toString()：非字符串头值（数字、布尔）统一转字符串。
		for key, raw := range typed {
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
		Source:      s.script.SourceBindings(ctx, src),
	})
	analyzer.LoadVariables(b.Variable)
	analyzer.LoadVariables(chapter.Variable)
	if strings.TrimSpace(contentRule.Title) != "" {
		if title, err := analyzer.GetString(ctx, contentRule.Title, body); err == nil && strings.TrimSpace(title) != "" {
			chapter.Title = strings.TrimSpace(title)
		}
	}
	// 正文不在规则层反转义实体，normalizeContent 在格式化后统一处理（对齐 Legado getString(content, unescape=false)）。
	content, err := analyzer.GetStringRaw(ctx, contentRule.Content, body)
	if err != nil {
		return "", nil, err
	}
	content = normalizeContent(content, redirectURL)
	nextURLs := []string{}
	if getNext && strings.TrimSpace(contentRule.NextContentURL) != "" {
		values, err := analyzer.GetURLList(ctx, contentRule.NextContentURL, body)
		if err != nil {
			return "", nil, err
		}
		nextURLs = append(nextURLs, values...)
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
		"source":         s.source,
		"book":           s.book,
		"chapter":        s.chapter,
		"title":          script.ChapterTitleBinding(s.chapter),
		"nextChapterUrl": s.nextChapterURL,
	})
}
