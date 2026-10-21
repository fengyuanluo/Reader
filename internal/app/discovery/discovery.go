package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	bookapp "reader/internal/app/book"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/rule"
	"reader/internal/domain/source"
	"reader/internal/infra/ratelimit"
	"reader/internal/infra/requestoption"
	ruleinfra "reader/internal/infra/rule"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/script"
)

var (
	ErrEmptyExploreURL = errors.New("exploreUrl is required")
	ErrEmptyDiscovery  = errors.New("discovery category is required")
)

type Category struct {
	Title string
	URL   string
}

type Input struct {
	SourceIDs    []string
	Group        string
	DiscoveryURL string
	Page         int
}

type Result struct {
	Items    []book.SearchBook
	Warnings []Warning
	// HasMore 表示本页有命中，可以继续请求下一页（发现页同样没有总数信息，空页即结束）。
	HasMore bool
}

type Warning struct {
	SourceID       string
	BookSourceURL  string
	BookSourceName string
	Code           string
	Message        string
}

// Service 提供书源分类（发现页签）与多源发现能力。
type Service struct {
	repo    source.BookSourceRepository
	fetcher fetch.Fetcher
	script  *script.Runtime
	limiter *ratelimit.Limiter
	cache   *categoryCache
	// categoryFlight 对同一 (源, exploreURL) 的分类解析做单飞（对齐 Legado per-source Mutex），
	// Discover 多源 + Categories 并发首次调用时同一 JS 脚本只执行一次（S14-10）。
	categoryFlight *singleflight
}

func NewService(repo source.BookSourceRepository, fetcher fetch.Fetcher, scriptRuntime *script.Runtime) *Service {
	return &Service{repo: repo, fetcher: fetcher, script: scriptRuntime, cache: newCategoryCache(defaultCategoryCacheTTL, defaultCategoryCacheMaxItems), categoryFlight: newSingleflight()}
}

// WithLimiter 注入全局与单源并发上限；按源 concurrentRate 的限速由 fetcher（fetch.Request.SourceKey/
// ConcurrentRate）负责，这里不再重复限速。多源发现并发度缺省取其全局并发上限。
func (s *Service) WithLimiter(limiter *ratelimit.Limiter) *Service {
	s.limiter = limiter
	return s
}

// WithCategoryCache 覆盖探索分类缓存参数（TTL 与最大条目数），仅用于测试或按需调优。
func (s *Service) WithCategoryCache(ttl time.Duration, maxItems int) *Service {
	s.cache = newCategoryCache(ttl, maxItems)
	return s
}

// WithCategoryFlight 覆盖分类单飞实现，仅用于测试。
func (s *Service) WithCategoryFlight(flight *singleflight) *Service {
	s.categoryFlight = flight
	return s
}

// InvalidateExploreKinds 清除指定书源的探索分类缓存（对齐 Legado removeExploreKinds），
// 书源导入/更新/删除后调用，避免继续返回旧分类。
func (s *Service) InvalidateExploreKinds(bookSourceURL string) {
	if s == nil || s.cache == nil {
		return
	}
	s.cache.invalidateSource(bookSourceURL)
}

func (s *Service) Categories(ctx context.Context, sourceID string) ([]Category, error) {
	ctx = script.WithStage(ctx, "discovery")
	src, err := s.repo.FindByID(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	return s.categoriesForSource(ctx, src)
}

func (s *Service) Discover(ctx context.Context, input Input) (Result, error) {
	ctx = script.WithStage(ctx, "discovery")
	sources, resolveWarnings, err := s.resolveSources(ctx, input)
	if err != nil {
		return Result{}, err
	}
	concurrency := s.limiter.GlobalLimit()
	if concurrency < 1 {
		concurrency = defaultDiscoverConcurrency
	}
	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex
	result := Result{Warnings: resolveWarnings}
	var wg sync.WaitGroup
	for _, src := range sources {
		src := src
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 单源 panic 只影响该源，转成 Warning，其余源结果照常返回。
			defer func() {
				if recovered := recover(); recovered != nil {
					mu.Lock()
					result.Warnings = append(result.Warnings, Warning{
						SourceID:       src.SourceID,
						BookSourceURL:  src.BookSourceURL,
						BookSourceName: src.BookSourceName,
						Code:           "discovery_panic",
						Message:        fmt.Sprintf("panic: %v", recovered),
					})
					mu.Unlock()
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()
			items, err := s.discoverSource(ctx, src, input)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Warnings = append(result.Warnings, Warning{
					SourceID:       src.SourceID,
					BookSourceURL:  src.BookSourceURL,
					BookSourceName: src.BookSourceName,
					Code:           "discovery_failed",
					Message:        err.Error(),
				})
				return
			}
			result.Items = append(result.Items, items...)
		}()
	}
	wg.Wait()
	result.HasMore = len(result.Items) > 0
	return result, nil
}

// defaultDiscoverConcurrency 为未配置任何并发参数时的兜底多源发现并发度。
const defaultDiscoverConcurrency = 4

// resolveSources 解析待发现书源；单个无效 sourceId 只跳过该源并记录 Warning，不中止整个请求。
func (s *Service) resolveSources(ctx context.Context, input Input) ([]*source.BookSource, []Warning, error) {
	if len(input.SourceIDs) > 0 {
		out := []*source.BookSource{}
		warnings := []Warning{}
		for _, id := range input.SourceIDs {
			src, err := s.repo.FindByID(ctx, id)
			if err != nil {
				warnings = append(warnings, Warning{SourceID: id, Code: "source_unavailable", Message: err.Error()})
				continue
			}
			if !src.EnabledExplore {
				continue
			}
			out = append(out, src)
		}
		return out, warnings, nil
	}
	enabled := true
	filter := source.BookSourceFilter{EnabledExplore: &enabled}
	if input.Group != "" {
		filter.Group = input.Group
	}
	items, err := source.ListAll(ctx, s.repo, filter)
	if err != nil {
		return nil, nil, err
	}
	return items, nil, nil
}

func (s *Service) discoverSource(ctx context.Context, src *source.BookSource, input Input) ([]book.SearchBook, error) {
	discoveryURL := strings.TrimSpace(input.DiscoveryURL)
	if discoveryURL == "" {
		categories, err := s.categoriesForSource(ctx, src)
		if err != nil {
			return nil, err
		}
		if len(categories) == 0 {
			return nil, ErrEmptyDiscovery
		}
		// 对齐 Legado CheckSourceService.kt:176-180：只取第一个 URL 非空的类目，不把中文类目名
		// 当 URL 发请求（Legado filter { !it.url.isNullOrBlank() }）。类目均无 URL 时回退用
		// exploreUrl 本身（对齐 Legado ExploreShowViewModel.kt:71 直接使用 exploreUrl），仍为空才失败。
		for _, category := range categories {
			if strings.TrimSpace(category.URL) != "" {
				discoveryURL = category.URL
				break
			}
		}
		if strings.TrimSpace(discoveryURL) == "" {
			discoveryURL = strings.TrimSpace(src.ExploreURL)
		}
		if strings.TrimSpace(discoveryURL) == "" {
			return nil, ErrEmptyDiscovery
		}
	}
	discoveryURL = buildPageURL(discoveryURL, input.Page)
	headers, err := sourceapp.NewScriptIntegration(s.script).HeaderMap(ctx, src)
	if err != nil {
		return nil, err
	}
	evaluator := sourceapp.RequestEvaluator(s.script, src, script.Bindings{
		"page": input.Page, "baseUrl": src.BookSourceURL,
	})
	req, err := requestoption.Build(ctx, discoveryURL, src.BookSourceURL, fetch.Request{
		Headers:        headers,
		UseCookieJar:   src.EnabledCookieJar,
		SourceKey:      src.BookSourceURL,
		ConcurrentRate: src.ConcurrentRate,
	}, evaluator)
	if err != nil {
		return nil, err
	}
	resp, err := ratelimit.Do(ctx, s.limiter, src.BookSourceURL, func(ctx context.Context) (*fetch.Response, error) {
		return requestoption.Fetch(ctx, s.fetcher, req, sourceapp.RequestEvaluator(s.script, src, script.Bindings{
			"page": input.Page, "baseUrl": req.URL,
		}))
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
	listRule, err := listRuleForSource(src)
	if err != nil {
		return nil, err
	}
	return parseBookList(ctx, src, listRule, resp.URL, string(resp.Body), s.script)
}

func (s *Service) categoriesForSource(ctx context.Context, src *source.BookSource) ([]Category, error) {
	raw := strings.TrimSpace(src.ExploreURL)
	if raw == "" {
		return nil, ErrEmptyExploreURL
	}
	if key, ok := categoryCacheKeyFor(src, raw); ok {
		// 单飞同 (源, exploreURL) 的解析（对齐 Legado per-source Mutex，S14-10）。
		return s.categoryFlight.do(key.sourceURL+"\x00"+key.exploreURL, func() ([]Category, error) {
			return s.resolveCategories(ctx, src, raw)
		})
	}
	return s.resolveCategories(ctx, src, raw)
}

func (s *Service) resolveCategories(ctx context.Context, src *source.BookSource, raw string) ([]Category, error) {
	if strings.HasPrefix(strings.ToLower(raw), "@js:") || strings.HasPrefix(strings.ToLower(raw), "<js>") {
		if cached, ok := s.loadCategoryCache(src, raw); ok {
			return cached, nil
		}
		if s.script == nil {
			return nil, ruleinfra.ErrScriptUnavailable
		}
		scriptText := raw
		if strings.HasPrefix(strings.ToLower(raw), "@js:") {
			scriptText = raw[4:]
		} else if end := strings.LastIndex(strings.ToLower(raw), "<"); end > 4 {
			// 对齐 Legado BookSourceExtensions.kt:60：从 4 之后起找最后一个 '<'，
			// 不要求完整 </js>（书源常见 <js>...<js2> 或注释残留也能解析）。
			scriptText = raw[4:end]
		}
		value, err := s.script.EvalWithBindings(ctx, scriptText, script.Bindings{"source": src})
		if err != nil {
			// 对齐 Legado BookSourceExtensions.kt:82：JS 失败返回 ERROR 类目，分类页仍可见错误信息，
			// 而不是让整个源失败。
			return []Category{{Title: "ERROR:" + err.Error()}}, nil
		}
		raw = strings.TrimSpace(toString(value))
	}
	if strings.HasPrefix(raw, "[") {
		var values []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		}
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return nil, err
		}
		out := make([]Category, 0, len(values))
		for _, value := range values {
			if strings.TrimSpace(value.Title) == "" && strings.TrimSpace(value.URL) == "" {
				continue
			}
			out = append(out, Category{Title: strings.TrimSpace(value.Title), URL: strings.TrimSpace(value.URL)})
		}
		s.storeCategoryCache(src, strings.TrimSpace(src.ExploreURL), out)
		return out, nil
	}
	parts := splitExploreKinds(raw)
	out := make([]Category, 0, len(parts))
	for _, part := range parts {
		cfg := strings.SplitN(part, "::", 2)
		category := Category{Title: strings.TrimSpace(cfg[0])}
		if len(cfg) > 1 {
			category.URL = strings.TrimSpace(cfg[1])
		}
		if category.Title != "" || category.URL != "" {
			out = append(out, category)
		}
	}
	s.storeCategoryCache(src, strings.TrimSpace(src.ExploreURL), out)
	return out, nil
}

func listRuleForSource(src *source.BookSource) (rule.SearchRule, error) {
	if src.RuleExplore.Value != nil && strings.TrimSpace(src.RuleExplore.Value.BookList) != "" {
		explore := src.RuleExplore.Value
		return rule.SearchRule{
			BookList:    explore.BookList,
			Name:        explore.Name,
			Author:      explore.Author,
			Intro:       explore.Intro,
			Kind:        explore.Kind,
			LastChapter: explore.LastChapter,
			UpdateTime:  explore.UpdateTime,
			BookURL:     explore.BookURL,
			CoverURL:    explore.CoverURL,
			WordCount:   explore.WordCount,
		}, nil
	}
	if src.RuleSearch.Value != nil && strings.TrimSpace(src.RuleSearch.Value.BookList) != "" {
		return *src.RuleSearch.Value, nil
	}
	return rule.SearchRule{}, errors.New("ruleExplore.bookList or ruleSearch.bookList is required")
}

func parseBookList(ctx context.Context, src *source.BookSource, listRule rule.SearchRule, redirectURL string, body string, scriptRuntime *script.Runtime) ([]book.SearchBook, error) {
	ruleText, reverse := normalizeDiscoveryListRule(listRule.BookList)
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		RedirectURL: redirectURL,
		SourceURL:   src.BookSourceURL,
		Script:      sourceapp.ScriptAdapter{Runtime: scriptRuntime, BaseURL: redirectURL, Source: src, Src: body},
		Source:      scriptRuntime.SourceBindings(ctx, src),
	})
	elements, err := analyzer.GetElements(ctx, ruleText, body)
	if err != nil {
		return nil, err
	}
	out := []book.SearchBook{}
	seen := map[string]struct{}{}
	baseVars := analyzer.Variables()
	// 对齐 Legado BookList.kt:250-290：element 循环外建一个 itemAnalyzer（单次规则编译），
	// 循环内仅重绑定书籍与基线变量，避免 条目数×规则数 次解析（S14-07）。
	sourceBindings := scriptRuntime.SourceBindings(ctx, src)
	itemAnalyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		RedirectURL: redirectURL,
		SourceURL:   src.BookSourceURL,
		Script:      sourceapp.ScriptAdapter{Runtime: scriptRuntime, BaseURL: redirectURL, Source: src, Src: body},
		Source:      sourceBindings,
	})
	for _, element := range elements {
		searchBook := book.SearchBook{
			Origin:      src.BookSourceURL,
			OriginName:  src.BookSourceName,
			Type:        src.BookSourceType,
			OriginOrder: src.CustomOrder,
			Origins:     []string{src.BookSourceURL},
			Variable:    encodeDiscoveryVariables(baseVars),
		}
		itemAnalyzer.SetBook(&searchBook, sourceapp.ScriptAdapter{Runtime: scriptRuntime, BaseURL: redirectURL, Source: src, Src: body, Book: &searchBook})
		itemAnalyzer.SetVariables(baseVars)
		name, _ := itemAnalyzer.GetString(ctx, listRule.Name, element)
		name = bookapp.FormatBookName(name)
		if name == "" {
			continue
		}
		author, _ := itemAnalyzer.GetString(ctx, listRule.Author, element)
		bookURL, _ := itemAnalyzer.GetURL(ctx, listRule.BookURL, element)
		if bookURL == "" {
			bookURL = redirectURL
		}
		if _, ok := seen[bookURL]; ok {
			continue
		}
		seen[bookURL] = struct{}{}
		kinds, _ := itemAnalyzer.GetStringList(ctx, listRule.Kind, element)
		intro, _ := itemAnalyzer.GetString(ctx, listRule.Intro, element)
		cover, _ := itemAnalyzer.GetURL(ctx, listRule.CoverURL, element)
		wordCount, _ := itemAnalyzer.GetString(ctx, listRule.WordCount, element)
		lastChapter, _ := itemAnalyzer.GetString(ctx, listRule.LastChapter, element)
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
		out = append(out, searchBook)
	}
	if reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	if len(out) == 0 && strings.TrimSpace(src.BookURLPattern) == "" {
		// 对齐 Legado BookList.kt:100-106：仅在未配置 bookUrlPattern 时才按详情页回退解析。
		preview, err := bookapp.PreviewSearchBookFromInfo(ctx, src, redirectURL, body, book.SearchBook{
			BookURL:     redirectURL,
			Origin:      src.BookSourceURL,
			OriginName:  src.BookSourceName,
			OriginOrder: src.CustomOrder,
			Type:        src.BookSourceType,
		}, scriptRuntime)
		if err == nil && strings.TrimSpace(preview.Name) != "" {
			return []book.SearchBook{preview}, nil
		}
	}
	return out, nil
}

func buildPageURL(template string, page int) string {
	if page < 1 {
		page = 1
	}
	pageText := strconv.Itoa(page)
	out := strings.ReplaceAll(template, "{{page}}", pageText)
	out = strings.ReplaceAll(out, "{page}", pageText)
	// 对齐 Legado AnalyzeUrl.replaceKeyPageJs：处理 <a,b,c> 分页列表。
	return urlrule.ReplacePagePlaceholders(out, page)
}

func splitExploreKinds(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' })
	out := []string{}
	for _, field := range fields {
		for _, part := range strings.Split(field, "&&") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	if str, ok := value.(string); ok {
		return str
	}
	data, _ := json.Marshal(value)
	return string(data)
}

func (s *Service) loadCategoryCache(src *source.BookSource, rawExploreURL string) ([]Category, bool) {
	if s.cache == nil {
		return nil, false
	}
	key, ok := categoryCacheKeyFor(src, rawExploreURL)
	if !ok {
		return nil, false
	}
	return s.cache.get(key)
}

func (s *Service) storeCategoryCache(src *source.BookSource, rawExploreURL string, categories []Category) {
	if s.cache == nil {
		return
	}
	key, ok := categoryCacheKeyFor(src, rawExploreURL)
	if !ok {
		return
	}
	s.cache.put(key, categories)
}

type categoryCacheKey struct {
	sourceURL  string
	exploreURL string
}

func categoryCacheKeyFor(src *source.BookSource, rawExploreURL string) (categoryCacheKey, bool) {
	if src == nil {
		return categoryCacheKey{}, false
	}
	sourceURL := strings.TrimSpace(src.BookSourceURL)
	if sourceURL == "" {
		return categoryCacheKey{}, false
	}
	return categoryCacheKey{sourceURL: sourceURL, exploreURL: strings.TrimSpace(rawExploreURL)}, true
}

func cloneCategories(categories []Category) []Category {
	if len(categories) == 0 {
		return nil
	}
	out := make([]Category, len(categories))
	copy(out, categories)
	return out
}

func normalizeDiscoveryListRule(rule string) (string, bool) {
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

func encodeDiscoveryVariables(vars map[string]string) string {
	if len(vars) == 0 {
		return ""
	}
	raw, err := json.Marshal(vars)
	if err != nil {
		return ""
	}
	return string(raw)
}
