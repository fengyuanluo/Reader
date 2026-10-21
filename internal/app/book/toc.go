package bookapp

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

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

// maxTocPages 是单次目录刷新的分页抓取上限；异常书源的 nextTocURL 可能自引用，
// 不加上限会静默无限抓取，超限时截断并记录 warning。
const maxTocPages = 100

var (
	ErrTocRuleMissing = errors.New("ruleToc is required")
	ErrTocEmpty       = errors.New("toc_empty")
	// ErrTocPaginationLimit 表示单次目录刷新的分页数超过 maxTocPages 被截断；
	// 此时目录不完整，调用方必须保留库内旧目录，不能用残缺结果整体替换。
	ErrTocPaginationLimit = errors.New("toc pagination exceeded limit, keeping existing chapters")
)

type TocInput struct {
	Source  *source.BookSource
	Book    book.Book
	TocHTML string
	// SkipSave 为真时只抓取并解析目录，不写 chapters 表；
	// 对应 Legado CheckSource / Debug 的 needSave=false 语义。
	SkipSave bool
}

type TocService struct {
	chapters book.ChapterRepository
	books    book.BookRepository
	fetcher  fetch.Fetcher
	script   *script.Runtime
}

func NewTocService(chapters book.ChapterRepository, fetcher fetch.Fetcher, scriptRuntime *script.Runtime) *TocService {
	return &TocService{chapters: chapters, fetcher: fetcher, script: scriptRuntime}
}

// WithBooks 注入 books 仓库（可选能力，类型断言/方法链注入）；目录刷新成功后
// 按其 UpdateTocStats 写回 total_chapter_num/last_check_*/latest_chapter_*/variable/dur_*，
// 对齐 Legado BookChapterList.kt:152-161 的书籍元数据更新。
func (s *TocService) WithBooks(books book.BookRepository) *TocService {
	s.books = books
	return s
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
		resp, err := executeBookFetchRequest(ctx, s.fetcher, req, src, &b, s.script)
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
	// 对齐 Legado BookChapterList.kt:113-119：非倒序目录先整体 reverse，再按完整键去重
	// （LinkedHashSet 保留最后一次出现），最后按目标方向反回。
	_, reverse := normalizeTocListRule(src.RuleToc.Value.ChapterList)
	if !reverse {
		reverseChapters(chapters)
	}
	chapters = dedupeChapters(chapters)
	if !reverse {
		reverseChapters(chapters)
	}
	for i, chapter := range chapters {
		chapter.Index = i
	}
	if err := s.formatChapters(ctx, src, chapters); err != nil {
		return nil, err
	}
	if s.chapters != nil && !input.SkipSave {
		bookID := book.IDFromURL(b.BookURL)
		if err := s.chapters.ReplaceByBook(ctx, bookID, chapters); err != nil {
			return nil, err
		}
		s.writeBackBookStats(ctx, input, b, chapters)
	}
	return chapters, nil
}

// tocStatsUpdate 是目录刷新后写回 books 表的可选仓库能力（类型断言注入）。
// 语义要求与 SqliteFix 的 BookRepository.UpdateTocStats 一致：只更新目录刷新相关列，
// Variable 仅在 VariableChanged 时写入，dur_* 仅在 DurChapterChanged 时写入，行不存在不创建。
// domain 接口由 SqliteFix 负责扩展；这里用本地接口解耦编译顺序。
type tocStatsUpdate interface {
	UpdateTocStats(ctx context.Context, bookID string, stats book.TocStats) error
}

// writeBackBookStats 在 ReplaceByBook 成功后写回书籍元数据（对齐 Legado
// BookChapterList.kt:152-161）：totalChapterNum、lastCheckTime、新增章数>0 时的
// lastCheckCount/latestChapterTime；preUpdateJS 修改的 variable（S12-05）与
// getDurChapter 重映射后的阅读进度（S12-01）同样随该更新持久化。
// 仓库不支持 UpdateTocStats（fake/旧实现）时静默跳过，不影响目录刷新结果。
func (s *TocService) writeBackBookStats(ctx context.Context, input TocInput, b book.Book, chapters []*book.BookChapter) {
	updater, ok := s.books.(tocStatsUpdate)
	if !ok {
		return
	}
	now := time.Now().UnixMilli()
	stats := book.TocStats{
		TotalChapterNum: len(chapters),
		LastCheckTime:   now,
	}
	if len(chapters) > b.TotalChapterNum {
		stats.LastCheckCount = len(chapters) - b.TotalChapterNum
		stats.LatestChapterTime = now
	}
	if len(chapters) > 0 {
		// 对齐 Legado BookChapterList.kt:150：durChapterTitle 取刷新后当前进度指向的章节标题。
		index := b.DurChapterIndex
		if index >= len(chapters) {
			index = len(chapters) - 1
		}
		if index < 0 {
			index = 0
		}
		stats.DurChapterTitle = chapters[index].Title
		stats.DurChapterIndex = getDurChapter(b.DurChapterIndex, b.DurChapterTitle, chapters, b.TotalChapterNum)
		stats.DurChapterPos = b.DurChapterPos
		stats.DurChapterTime = b.DurChapterTime
		stats.DurChapterChanged = stats.DurChapterIndex != b.DurChapterIndex || stats.DurChapterTitle != b.DurChapterTitle
	}
	if b.Variable != input.Book.Variable {
		stats.Variable = b.Variable
		stats.VariableChanged = true
	}
	if err := updater.UpdateTocStats(ctx, book.IDFromURL(b.BookURL), stats); err != nil {
		// 元数据写回失败不影响目录刷新结果；Legado 同样只记日志继续（BookChapterList.kt 的 upsertBook 容错）。
		slog.Warn("toc stats writeback failed", "source", input.Source.BookSourceURL, "book", b.BookURL, "error", err)
	}
}

// FirstContentChapter 返回第一个非卷章节及其 nextChapterURL；全是卷时取第 0 章。
// nextChapterURL 取切片中下一章 URL，末章回退第 0 章 URL
// （对齐 contentapp 的 GetChapterContent 与 Legado BookContent.kt:52-56）。
func FirstContentChapter(chapters []*book.BookChapter) (book.BookChapter, string, bool) {
	if len(chapters) == 0 {
		return book.BookChapter{}, "", false
	}
	pos := -1
	for i, chapter := range chapters {
		if chapter == nil || chapter.IsVolume {
			continue
		}
		pos = i
		break
	}
	if pos == -1 {
		// 全是卷（或全为 nil）时取第 0 章；第 0 个元素为 nil 视为无章节。
		if chapters[0] == nil {
			return book.BookChapter{}, "", false
		}
		return *chapters[0], nextChapterURLOf(chapters, 0), true
	}
	return *chapters[pos], nextChapterURLOf(chapters, pos), true
}

// nextChapterURLOf 取第 index 章的下一章 URL；index 越界或下一章为 nil 时回退第 0 章 URL，
// 末章同样回退第 0 章（对齐 Legado BookContent.kt:52-56 的 nextChapterUrl 取值方式）。
func nextChapterURLOf(chapters []*book.BookChapter, index int) string {
	nextIndex := index + 1
	if nextIndex < 0 || nextIndex >= len(chapters) || chapters[nextIndex] == nil {
		nextIndex = 0
	}
	if chapters[nextIndex] == nil {
		return ""
	}
	return chapters[nextIndex].URL
}

// incompleteTocError 汇总并发分页阶段的失败；任何一个分页失败都意味着目录不完整，
// 调用方必须保留库内旧目录，不能用残缺结果整体替换（C-016 同类修复）。
type incompleteTocError struct{ failures []string }

func (e *incompleteTocError) Error() string {
	return "toc page(s) failed, keeping existing chapters: " + strings.Join(e.failures, "; ")
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
		if len(seenPages) >= maxTocPages {
			slog.Warn("toc pagination exceeded limit, keeping existing chapters", "source", src.BookSourceURL, "limit", maxTocPages)
			return nil, ErrTocPaginationLimit
		}
		next := nextURLs[0]
		if next == "" {
			break
		}
		if _, ok := seenPages[next]; ok {
			break
		}
		seenPages[next] = struct{}{}
		req, err := buildBookFetchRequest(ctx, next, src, b, s.script)
		if err != nil {
			return nil, err
		}
		resp, err := executeBookFetchRequest(ctx, s.fetcher, req, src, b, s.script)
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
		// 对齐串行链（C-015/C-016）：并发分支同样受 maxTocPages 截断，异常规则一次返回
		// 数百个 nextURL 时不加限制会长时间占用抓取与内存（S12-07）；目录不完整则不覆盖。
		if len(seenPages) > maxTocPages {
			slog.Warn("toc pagination exceeded limit, keeping existing chapters", "source", src.BookSourceURL, "limit", maxTocPages)
			return nil, ErrTocPaginationLimit
		}
		// 对齐 Legado BookChapterList.kt:101-113：多分页并发抓取，章节按规则返回顺序拼接。
		// 每个分页从请求构建到解析全程用 book 快照（S12-08），避免 URL 选项 JS 通过 book
		// 绑定并发写同一 Go 结构体造成数据竞争。
		// 分页失败会收集起来：目录不完整时不能覆盖库内目录（见 RefreshToc）。
		var mu sync.Mutex
		var failures []string
		recordFailure := func(stage string, target string, err error) {
			mu.Lock()
			failures = append(failures, stage+" "+target+": "+err.Error())
			mu.Unlock()
			slog.Warn("toc page "+stage+" failed", "source", src.BookSourceURL, "url", target, "error", err)
		}
		pageChapters, err := concurrency.Map(ctx, tocPageConcurrency, targets, func(ctx context.Context, _ int, target string) ([]*book.BookChapter, error) {
			pageBook := *b
			req, err := buildBookFetchRequest(ctx, target, src, &pageBook, s.script)
			if err != nil {
				recordFailure("fetch", target, err)
				return nil, nil
			}
			resp, err := executeBookFetchRequest(ctx, s.fetcher, req, src, &pageBook, s.script)
			if err != nil {
				recordFailure("fetch", target, err)
				return nil, nil
			}
			if s.script != nil {
				resp, err = sourceapp.NewScriptIntegration(s.script).LoginCheck(ctx, src, resp)
				if err != nil {
					recordFailure("login check", target, err)
					return nil, nil
				}
			}
			parsed, _, err := parseTocPage(ctx, src, &pageBook, target, resp.URL, string(resp.Body), listRule, false, s.script)
			if err != nil {
				recordFailure("parse", target, err)
				return nil, nil
			}
			return parsed, nil
		})
		if err != nil {
			return nil, err
		}
		if len(failures) > 0 {
			return nil, &incompleteTocError{failures: failures}
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
		Script:      sourceapp.ScriptAdapter{Runtime: scriptRuntime, BaseURL: redirectURL, Source: src, Book: b, Src: body},
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
	// 对齐 Legado BookChapterList.kt:216-230：每页只建一个 analyzeRule，逐章 setContent(item)。
	// Analyzer 在章节间仅替换 Chapter 载体（S12-06）；Source 绑定与顶层 baseVars 每页构建一次，
	// 不再逐章重建十几项的 map（万章目录 = 万次 Analyzer 分配 + 2 万次 map 构建 → 每页 2 次）。
	itemAnalyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		RedirectURL: redirectURL,
		SourceURL:   src.BookSourceURL,
		// 对齐 Legado BookChapterList.kt:220-224：逐章先 setChapter 再解析标题/链接等规则，
		// 脚本里的 chapter 与顶层 title 绑定取当前章节（title 在标题规则解析后即可用）。
		Script: sourceapp.ScriptAdapter{Runtime: scriptRuntime, BaseURL: redirectURL, Source: src, Book: b, Src: body},
		Book:   b,
		Source: scriptRuntime.SourceBindings(ctx, src),
	})
	itemAnalyzer.SetVariables(baseVars)
	for index, element := range elements {
		chapter := &book.BookChapter{
			BaseURL: redirectURL,
			BookURL: b.BookURL,
		}
		// 对齐 Legado BookChapterList.kt:216-230 的 analyzeRule.setChapter(item)：
		// 每页复用同一 Analyzer，逐章仅替换 Chapter 载体与脚本适配器（S12-06）；
		// 基线变量恢复由 SetChapter 内部完成，chapter 级 @put 不跨章泄漏。
		itemAnalyzer.SetChapter(chapter, sourceapp.ScriptAdapter{Runtime: scriptRuntime, BaseURL: redirectURL, Source: src, Book: b, Chapter: chapter, Src: body})
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

// dedupeChapters 对齐 Legado LinkedHashSet(chapterList) 的 data class equals 语义：
// 键为完整可比较字段（url+title+tag+isVolume/isVip/isPay），同 URL 但附加信息
// （tag/VIP/卷标）不同的两条都会保留，不再按 URL 单字段误删。
func dedupeChapters(chapters []*book.BookChapter) []*book.BookChapter {
	out := []*book.BookChapter{}
	type dedupeKey struct {
		url      string
		title    string
		tag      string
		isVolume bool
		isVIP    bool
		isPay    bool
	}
	seen := map[dedupeKey]struct{}{}
	for _, chapter := range chapters {
		if chapter == nil {
			continue
		}
		key := dedupeKey{
			url:      chapter.URL,
			title:    chapter.Title,
			tag:      chapter.Tag,
			isVolume: chapter.IsVolume,
			isVIP:    chapter.IsVIP,
			isPay:    chapter.IsPay,
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
