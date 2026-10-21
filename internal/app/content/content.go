package contentapp

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sync/singleflight"
	replaceapp "reader/internal/app/replace"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/source"
	"reader/internal/infra/concurrency"
	"reader/internal/infra/requestoption"
	ruleinfra "reader/internal/infra/rule"
	"reader/internal/infra/rule/regex"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/script"
)

// pageConcurrency 是正文多分页并发抓取的 worker 上限；Legado 用 AppConfig.threadCount（默认 16），
// Reader 保守取 8，避免同时打满单个书源站点。
const pageConcurrency = 8

// maxContentPages 是单章正文分页抓取的页数上限；异常书源的 nextTocURL 可能自引用，
// 不加上限会静默无限抓取，超限时截断并记录 warning。
const maxContentPages = 100

var (
	ErrContentRuleMissing            = errors.New("ruleContent is required")
	ErrContentEmpty                  = errors.New("content_empty")
	ErrUnsupportedContentBrowserFlow = errors.New("content sourceRegex requires unsupported browser flow")
	// ErrPaginationTruncated 表示单章分页超过 maxContentPages 上限（异常书源自引用），
	// 整章未完整抓取即失败：不写缓存，避免残缺正文被永久缓存。
	ErrPaginationTruncated = errors.New("content pagination exceeded limit")
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
	// CacheLookupDone 为真表示调用方已确认缓存未命中，内部不再重复查缓存，
	// 但抓取成功后仍照旧写入缓存；下载与探测热路径专用。
	CacheLookupDone bool
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
	// inflight 合并同一章节的并发抓取（S13-05）：预下载与阅读同时到达时共享一次
	// 多页抓取结果，避免重复带宽消耗与对书源站点的请求放大、缓存双写。
	inflight singleflight.Group
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
	contentRule := src.RuleContent.Value
	if strings.TrimSpace(contentRule.SubContent) != "" || strings.TrimSpace(contentRule.CallBackJS) != "" {
		slog.WarnContext(ctx, "Legado content extension rules are preserved but not executed",
			"source_id", src.SourceID,
			"sub_content", strings.TrimSpace(contentRule.SubContent) != "",
			"callback_js", strings.TrimSpace(contentRule.CallBackJS) != "",
		)
	}
	// contentRule.sourceRegex 在 Legado 中只由 BackstageWebView 通过 WebViewClient.onLoadResource
	// 嗅探页面加载的资源 URL（BackstageWebView.kt:283-296）并把命中的 URL 当作响应体返回，
	// 没有浏览器拿不到这些资源请求；本项目明确不引入 webView，因此统一返回“不支持”而不是静默忽略规则。
	if strings.TrimSpace(contentRule.SourceRegex) != "" {
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
	if s.chapters != nil && !input.CacheLookupDone {
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
	// S13-05：同一章节的并发请求（如预下载与阅读同时到达）以 bookID:index 合并抓取，
	// 共享一次完整多页抓取结果，避免重复带宽与缓存双写。
	type fetchOutcome struct {
		content string
		chapter book.BookChapter
	}
	outcomeAny, err, _ := s.inflight.Do(strings.Join([]string{bookID, strconv.Itoa(chapter.Index)}, "\x1f"), func() (any, error) {
		content, err := s.fetchChapterContent(ctx, src, &bookValue, &chapter, chapterURL, input.NextChapterURL)
		if err != nil {
			return nil, err
		}
		return fetchOutcome{content: content, chapter: chapter}, nil
	})
	if err != nil {
		return Result{}, err
	}
	outcome := outcomeAny.(fetchOutcome)
	chapter = outcome.chapter
	content := outcome.content
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

// fetchChapterContent 执行一次完整的多页正文抓取（缓存未命中路径）：
// TocHTML 内联解析或请求抓取 + 登录检查 + WebJS + 分页解析与替换。
// 由 GetContent 经 singleflight 调用（S13-05），同一章节并发请求只执行一次。
func (s *Service) fetchChapterContent(ctx context.Context, src *source.BookSource, b *book.Book, chapter *book.BookChapter, chapterURL string, nextChapterURL string) (string, error) {
	if chapterURL == b.BookURL && strings.TrimSpace(b.TocHTML) != "" {
		return s.parseAllPages(ctx, src, b, chapter, chapterURL, chapterURL, b.TocHTML, nextChapterURL)
	}
	expander := ruleinfra.NewAnalyzer(ruleinfra.Context{
		BaseURL:     chapter.BaseURL,
		RedirectURL: chapter.BaseURL,
		SourceURL:   src.BookSourceURL,
		Script:      sourceapp.ScriptAdapter{Runtime: s.script, BaseURL: chapter.BaseURL, Source: src, Book: b, Chapter: chapter},
		Chapter:     chapter,
		Book:        b,
		Source:      s.script.SourceBindings(ctx, src),
	})
	expander.LoadVariables(b.Variable)
	expander.LoadVariables(chapter.Variable)
	if expanded, err := expander.ExpandString(ctx, chapterURL, map[string]any{}); err == nil && strings.TrimSpace(expanded) != "" {
		chapterURL = expanded
	}
	req, err := buildContentFetchRequest(ctx, chapterURL, src, b, chapter, s.script)
	if err != nil {
		return "", err
	}
	if len(req.Body) > 0 {
		if expandedBody, err := expander.ExpandString(ctx, string(req.Body), map[string]any{}); err == nil && strings.TrimSpace(expandedBody) != "" {
			req.Body = []byte(expandedBody)
		}
	}
	resp, err := executeContentFetchRequest(ctx, s.fetcher, req, src, b, chapter, nextChapterURL, s.script)
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
	return s.parseAllPages(ctx, src, b, chapter, req.URL, resp.URL, string(resp.Body), nextChapterURL)
}

func (s *Service) parseAllPages(ctx context.Context, src *source.BookSource, b *book.Book, chapter *book.BookChapter, baseURL string, redirectURL string, body string, nextChapterURL string) (string, error) {
	content, nextURLs, err := s.parsePage(ctx, src, b, chapter, baseURL, redirectURL, body, true, true, nextChapterURL)
	if err != nil {
		return "", err
	}
	pages := []string{content}
	seen := map[string]struct{}{redirectURL: {}}
	for len(nextURLs) == 1 {
		if len(pages) >= maxContentPages {
			slog.WarnContext(ctx, "content pagination exceeded limit, failing chapter",
				"source_id", src.BookSourceURL, "limit", maxContentPages)
			return "", ErrPaginationTruncated
		}
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
		pageContent, pageNext, err := s.parsePage(ctx, src, b, chapter, next, resp.URL, string(resp.Body), true, false, nextChapterURL)
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
		// 列表型 nextContentUrl 规则可能返回任意多行，与串行分支同一上限语义：
		// 超限即整章失败，不对同一书源站点发起无界请求。
		if len(targets) > maxContentPages {
			slog.WarnContext(ctx, "content pagination exceeded limit, failing chapter",
				"source_id", src.BookSourceURL, "limit", maxContentPages, "targets", len(targets))
			return "", ErrPaginationTruncated
		}
		// 每个分页用 book/chapter 快照，避免并发解析时脚本写变量造成数据竞争。
		// 单个分页失败即整章失败：不写缓存、向上报错，避免残缺正文被当作成功持久化，
		// 下次命中缓存后永远不会再重抓。首页失败同样由上方直接返回错误。
		pageContents, err := concurrency.Map(ctx, pageConcurrency, targets, func(ctx context.Context, _ int, target string) (string, error) {
			pageBook := *b
			pageChapter := *chapter
			resp, err := s.fetchPage(ctx, src, &pageBook, &pageChapter, target, nextChapterURL)
			if err != nil {
				slog.WarnContext(ctx, "content page fetch failed", "source_id", src.BookSourceURL, "url", target, "error", err)
				return "", err
			}
			pageContent, _, err := s.parsePage(ctx, src, &pageBook, &pageChapter, target, resp.URL, string(resp.Body), false, false, nextChapterURL)
			if err != nil {
				slog.WarnContext(ctx, "content page parse failed", "source_id", src.BookSourceURL, "url", target, "error", err)
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
		// 对齐 Legado BookContent.kt:133-139：替换前逐行 trim，替换后每行加“　　”全角缩进。
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			lines[i] = strings.TrimSpace(line)
		}
		content = strings.Join(lines, "\n")
		replaced, err := applyContentReplace(ctx, redirectURL, src.RuleContent.Value.ReplaceRegex, content, s.script)
		if err != nil {
			return "", err
		}
		// 对齐 Legado BookContent.kt:133-139：替换前逐行 trim，替换后每行加“　　”全角缩进，
		// 末尾空行去除（BookContent.kt:139 trim()）。
		trimmed := strings.Split(replaced, "\n")
		out := make([]string, 0, len(trimmed))
		for _, line := range trimmed {
			out = append(out, "\u3000\u3000"+strings.TrimSpace(line))
		}
		content = strings.TrimRight(strings.Join(out, "\n"), "\u3000\n")
	}
	return content, nil
}

// fetchPage 抓取单个分页 URL，并按 Legado BookContent.kt 对每一页依次应用登录检查与正文 WebJS。
func (s *Service) fetchPage(ctx context.Context, src *source.BookSource, b *book.Book, chapter *book.BookChapter, url string, nextChapterURL string) (*fetch.Response, error) {
	req, err := buildContentFetchRequest(ctx, url, src, b, chapter, s.script)
	if err != nil {
		return nil, err
	}
	resp, err := executeContentFetchRequest(ctx, s.fetcher, req, src, b, chapter, nextChapterURL, s.script)
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
	// S13-07：同一响应体只转换一次并复用，避免逐绑定各拷贝一次。
	respBody := string(resp.Body)
	value, err := s.script.EvalWithBindings(ctx, scriptText, script.Bindings{
		// S13-07：同一响应体只转换一次，三个绑定共享同一个 string，避免 3 次拷贝。
		"body":           respBody,
		"result":         respBody,
		"src":            respBody,
		"baseUrl":        resp.URL,
		"source":         src,
		"book":           b,
		"chapter":        chapter,
		"title":          script.ChapterTitleBinding(chapter),
		"nextChapterUrl": nextChapterURL,
		"response": map[string]any{
			"statusCode": resp.StatusCode,
			"body":       respBody,
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

func buildContentFetchRequest(ctx context.Context, rawURL string, src *source.BookSource, b *book.Book, chapter *book.BookChapter, scriptRuntime *script.Runtime) (fetch.Request, error) {
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
	bindings := script.Bindings{"baseUrl": rawURL}
	if b != nil {
		bindings["book"] = b
	}
	if chapter != nil {
		bindings["chapter"] = chapter
	}
	return requestoption.Build(ctx, rawURL, src.BookSourceURL, req, sourceapp.RequestEvaluator(scriptRuntime, src, bindings))
}

func executeContentFetchRequest(ctx context.Context, fetcher fetch.Fetcher, req fetch.Request, src *source.BookSource, b *book.Book, chapter *book.BookChapter, nextChapterURL string, runtime *script.Runtime) (*fetch.Response, error) {
	bindings := script.Bindings{"baseUrl": req.URL, "nextChapterUrl": nextChapterURL}
	if b != nil {
		bindings["book"] = b
	}
	if chapter != nil {
		bindings["chapter"] = chapter
	}
	return requestoption.Fetch(ctx, fetcher, req, sourceapp.RequestEvaluator(runtime, src, bindings))
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
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{RedirectURL: redirectURL, Script: sourceapp.ScriptAdapter{Runtime: scriptRuntime, BaseURL: redirectURL}})
	return analyzer.GetString(ctx, rule, content)
}

func (s *Service) parsePage(ctx context.Context, src *source.BookSource, b *book.Book, chapter *book.BookChapter, baseURL string, redirectURL string, body string, getNext bool, applyTitle bool, nextChapterURL string) (string, []string, error) {
	contentRule := src.RuleContent.Value
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		BaseURL:     baseURL,
		RedirectURL: redirectURL,
		SourceURL:   src.BookSourceURL,
		Script:      sourceapp.ScriptAdapter{Runtime: s.script, BaseURL: baseURL, Source: src, Book: b, Chapter: chapter, Src: body, NextChapterURL: nextChapterURL},
		Chapter:     chapter,
		Book:        b,
		Source:      s.script.SourceBindings(ctx, src),
	})
	analyzer.LoadVariables(b.Variable)
	analyzer.LoadVariables(chapter.Variable)
	// 对齐 Legado BookContent.kt:60-71：title 规则只在最外层对首页执行一次，
	// 分页末页解析出的“第N页/上一页 下一页”类文本不得覆盖首页章节标题。
	if applyTitle && strings.TrimSpace(contentRule.Title) != "" {
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
	// S13-07：单次扫描写入 builder，替代 Split/Join 与逐行 TrimSpace 的多轮中间串分配。
	var out strings.Builder
	out.Grow(len(content))
	for line := range strings.Lines(content) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	result := out.String()
	return strings.TrimSuffix(result, "\n")
}

// imgPlaceholder 为图片占位符的不可打印控制字符哨兵（S13-08）：正文文本不会自然出现
// \x00 包裹的字面串，避免书源正文含 "__READER_IMG_N__" 字面文本时被误替换成图片 HTML。
func imgPlaceholder(index int) string {
	return "\x00READER_IMG_" + strconv.Itoa(index) + "\x00"
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
		return imgPlaceholder(len(images) - 1)
	})
	content = contentCommentPattern.ReplaceAllString(content, "")
	content = contentBlockTagPattern.ReplaceAllString(content, "\n")
	content = contentOtherHTMLPattern.ReplaceAllString(content, "")
	content = contentTrimStartPattern.ReplaceAllString(content, "")
	content = contentTrimEndPattern.ReplaceAllString(content, "")
	content = contentLineBreakPattern.ReplaceAllString(content, "\n")
	// S13-07：单次 NewReplacer 还原全部图片占位符，替代每张图一次全文 strings.ReplaceAll。
	pairs := make([]string, 0, len(images)*2)
	for i, image := range images {
		pairs = append(pairs, imgPlaceholder(i), image)
	}
	return strings.NewReplacer(pairs...).Replace(content)
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
