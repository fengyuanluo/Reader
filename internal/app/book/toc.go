package bookapp

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/source"
	"reader/internal/infra/concurrency"
	ruleinfra "reader/internal/infra/rule"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/script"
)

// tocPageConcurrency 是目录多分页并发抓取的 worker 上限；Legado 用 AppConfig.threadCount（默认 16），
// Reader 保守取 8，避免同时打满单个书源站点。
const tocPageConcurrency = 8

var (
	ErrTocRuleMissing = errors.New("ruleToc is required")
	ErrTocEmpty       = errors.New("toc_empty")
)

type TocInput struct {
	Source  *source.BookSource
	Book    book.Book
	TocHTML string
}

type TocService struct {
	chapters book.ChapterRepository
	fetcher  fetch.Fetcher
	script   *script.Runtime
}

func NewTocService(chapters book.ChapterRepository, fetcher fetch.Fetcher, scriptRuntime *script.Runtime) *TocService {
	return &TocService{chapters: chapters, fetcher: fetcher, script: scriptRuntime}
}

func (s *TocService) RefreshToc(ctx context.Context, input TocInput) ([]*book.BookChapter, error) {
	ctx = script.WithStage(ctx, "toc")
	src := input.Source
	if src == nil || src.RuleToc.Value == nil {
		return nil, ErrTocRuleMissing
	}
	b := input.Book
	if strings.TrimSpace(src.RuleToc.Value.PreUpdateJS) != "" && s.script != nil {
		if _, err := s.script.EvalWithBindings(script.WithSubStage(ctx, "pre_update_js"), src.RuleToc.Value.PreUpdateJS, script.Bindings{"source": src, "book": &b}); err != nil {
			return nil, err
		}
	}
	body := input.TocHTML
	baseURL := b.TocURL
	redirectURL := b.TocURL
	if body == "" {
		req, err := prepareBookFetchRequest(ctx, b.TocURL, src, &b, s.script)
		if err != nil {
			return nil, err
		}
		resp, err := s.fetcher.Fetch(ctx, req)
		if err != nil {
			return nil, err
		}
		if s.script != nil {
			resp, err = sourceapp.NewScriptIntegration(s.script).LoginCheck(ctx, src, resp)
			if err != nil {
				return nil, err
			}
		}
		body = string(resp.Body)
		redirectURL = resp.URL
	}
	chapters, err := s.parseAllPages(ctx, src, &b, baseURL, redirectURL, body)
	if err != nil {
		return nil, err
	}
	if len(chapters) == 0 {
		return nil, ErrTocEmpty
	}
	chapters = dedupeChapters(chapters)
	for i, chapter := range chapters {
		chapter.Index = i
	}
	if err := s.formatChapters(ctx, src, chapters); err != nil {
		return nil, err
	}
	if s.chapters != nil {
		if err := s.chapters.ReplaceByBook(ctx, book.IDFromURL(b.BookURL), chapters); err != nil {
			return nil, err
		}
	}
	return chapters, nil
}

func (s *TocService) parseAllPages(ctx context.Context, src *source.BookSource, b *book.Book, baseURL string, redirectURL string, body string) ([]*book.BookChapter, error) {
	tocRule := src.RuleToc.Value
	listRule, reverse := normalizeTocListRule(tocRule.ChapterList)
	chapters, nextURLs, err := parseTocPage(ctx, src, b, baseURL, redirectURL, body, listRule, true, s.script)
	if err != nil {
		return nil, err
	}
	seenPages := map[string]struct{}{redirectURL: {}}
	for len(nextURLs) == 1 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next := nextURLs[0]
		if next == "" {
			break
		}
		if _, ok := seenPages[next]; ok {
			break
		}
		seenPages[next] = struct{}{}
		req, err := buildBookFetchRequest(ctx, next, src, s.script)
		if err != nil {
			return nil, err
		}
		resp, err := s.fetcher.Fetch(ctx, req)
		if err != nil {
			return nil, err
		}
		if s.script != nil {
			resp, err = sourceapp.NewScriptIntegration(s.script).LoginCheck(ctx, src, resp)
			if err != nil {
				return nil, err
			}
		}
		pageChapters, pageNext, err := parseTocPage(ctx, src, b, next, resp.URL, string(resp.Body), listRule, true, s.script)
		if err != nil {
			return nil, err
		}
		chapters = append(chapters, pageChapters...)
		nextURLs = pageNext
	}
	if len(nextURLs) > 1 {
		targets := make([]string, 0, len(nextURLs))
		for _, next := range nextURLs {
			if next == "" {
				continue
			}
			if _, ok := seenPages[next]; ok {
				continue
			}
			seenPages[next] = struct{}{}
			targets = append(targets, next)
		}
		// 对齐 Legado BookChapterList.kt:101-113：多分页并发抓取，章节按规则返回顺序拼接。
		// 每个分页用 book 快照，避免脚本并发写书变量造成数据竞争。
		pageChapters, err := concurrency.Map(ctx, tocPageConcurrency, targets, func(ctx context.Context, _ int, target string) ([]*book.BookChapter, error) {
			req, err := buildBookFetchRequest(ctx, target, src, s.script)
			if err != nil {
				return nil, err
			}
			resp, err := s.fetcher.Fetch(ctx, req)
			if err != nil {
				return nil, err
			}
			if s.script != nil {
				resp, err = sourceapp.NewScriptIntegration(s.script).LoginCheck(ctx, src, resp)
				if err != nil {
					return nil, err
				}
			}
			pageBook := *b
			parsed, _, err := parseTocPage(ctx, src, &pageBook, target, resp.URL, string(resp.Body), listRule, false, s.script)
			return parsed, err
		})
		if err != nil {
			return nil, err
		}
		for _, page := range pageChapters {
			chapters = append(chapters, page...)
		}
	}
	if reverse {
		reverseChapters(chapters)
	}
	return chapters, nil
}

func parseTocPage(ctx context.Context, src *source.BookSource, b *book.Book, baseURL string, redirectURL string, body string, listRule string, getNextURL bool, scriptRuntime *script.Runtime) ([]*book.BookChapter, []string, error) {
	tocRule := src.RuleToc.Value
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		RedirectURL: redirectURL,
		SourceURL:   src.BookSourceURL,
		Script:      scriptAdapter{runtime: scriptRuntime, baseURL: redirectURL, source: src, book: b, src: body},
		Book:        b,
		Source:      scriptRuntime.SourceBindings(ctx, src),
	})
	analyzer.LoadVariables(b.Variable)
	elements, err := analyzer.GetElements(ctx, listRule, body)
	if err != nil {
		return nil, nil, err
	}
	nextURLs := []string{}
	if getNextURL && strings.TrimSpace(tocRule.NextTocURL) != "" {
		values, err := analyzer.GetURLList(ctx, tocRule.NextTocURL, body)
		if err != nil {
			return nil, nil, err
		}
		for _, value := range values {
			if value != redirectURL {
				nextURLs = append(nextURLs, value)
			}
		}
	}
	out := []*book.BookChapter{}
	baseVars := analyzer.Variables()
	for index, element := range elements {
		chapter := &book.BookChapter{
			BaseURL: redirectURL,
			BookURL: b.BookURL,
		}
		itemAnalyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
			RedirectURL: redirectURL,
			SourceURL:   src.BookSourceURL,
			// 对齐 Legado BookChapterList.kt:220-224：逐章先 setChapter 再解析标题/链接等规则，
			// 脚本里的 chapter 与顶层 title 绑定取当前章节（title 在标题规则解析后即可用）。
			Script:  scriptAdapter{runtime: scriptRuntime, baseURL: redirectURL, source: src, book: b, chapter: chapter, src: body},
			Chapter: chapter,
			Book:    b,
			Source:  scriptRuntime.SourceBindings(ctx, src),
		})
		itemAnalyzer.SetVariables(baseVars)
		title, _ := itemAnalyzer.GetString(ctx, tocRule.ChapterName, element)
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		rawURL := firstNonEmptyChapterURL(ctx, itemAnalyzer, tocRule.ChapterURL, element)
		tag, _ := itemAnalyzer.GetString(ctx, tocRule.UpdateTime, element)
		isVolumeText, _ := itemAnalyzer.GetString(ctx, tocRule.IsVolume, element)
		isVIPText, _ := itemAnalyzer.GetString(ctx, tocRule.IsVIP, element)
		isPayText, _ := itemAnalyzer.GetString(ctx, tocRule.IsPay, element)
		chapter.URL = rawURL
		chapter.Title = title
		chapter.IsVolume = isTruthy(isVolumeText)
		chapter.IsVIP = isTruthy(isVIPText)
		chapter.IsPay = isTruthy(isPayText)
		chapter.Tag = strings.TrimSpace(tag)
		if chapter.URL == "" {
			if chapter.IsVolume {
				chapter.URL = chapter.Title + strconv.Itoa(index)
			} else {
				chapter.URL = baseURL
			}
		}
		if !chapter.IsVolume {
			chapter.URL = urlrule.Absolute(redirectURL, chapter.URL)
		}
		out = append(out, chapter)
	}
	return out, nextURLs, nil
}

func firstNonEmptyChapterURL(ctx context.Context, analyzer *ruleinfra.Analyzer, rule string, input any) string {
	values, err := analyzer.GetStringList(ctx, rule, input)
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

func (s *TocService) formatChapters(ctx context.Context, src *source.BookSource, chapters []*book.BookChapter) error {
	ctx = script.WithSubStage(ctx, "format_js")
	formatJS := strings.TrimSpace(src.RuleToc.Value.FormatJS)
	if formatJS == "" || s.script == nil {
		return nil
	}
	for i, chapter := range chapters {
		value, err := s.script.EvalWithBindings(ctx, formatJS, script.Bindings{
			"source":  src,
			"chapter": chapter,
			"title":   chapter.Title,
			"index":   i + 1,
			"gInt":    0,
		})
		if err != nil {
			// 对齐 Legado BookChapterList.kt:135-146：单章格式出错只记日志并保留原标题，不中断整次目录刷新。
			slog.Warn("toc formatJs failed", "source", src.BookSourceURL, "chapterIndex", i, "error", err)
			continue
		}
		if title, ok := value.(string); ok && strings.TrimSpace(title) != "" {
			chapter.Title = title
		}
	}
	return nil
}

func dedupeChapters(chapters []*book.BookChapter) []*book.BookChapter {
	out := []*book.BookChapter{}
	seen := map[string]struct{}{}
	for _, chapter := range chapters {
		key := chapter.URL
		if key == "" {
			key = chapter.Title
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, chapter)
	}
	return out
}

func normalizeTocListRule(rule string) (string, bool) {
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

func reverseChapters(chapters []*book.BookChapter) {
	for i, j := 0, len(chapters)-1; i < j; i, j = i+1, j-1 {
		chapters[i], chapters[j] = chapters[j], chapters[i]
	}
}

// falseLikePattern 对应 Legado StringExtensions.kt:74-79 的 (?i)^(false|no|not|0)$。
var falseLikePattern = regexp.MustCompile(`(?i)^(false|no|not|0)$`)

// isTruthy 对齐 Legado String?.isTrue(nullIsTrue = false)：空值或字面量 "null" 为 false，
// 其余只要不是 false/no/not/0 即为 true（书源常用“VIP”“是”表示真值）。
func isTruthy(value string) bool {
	if strings.TrimSpace(value) == "" || value == "null" {
		return false
	}
	return !falseLikePattern.MatchString(strings.TrimSpace(value))
}
