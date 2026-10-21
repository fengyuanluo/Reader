package searchapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"reader/internal/domain/book"
	"reader/internal/domain/source"
	"reader/internal/infra/ratelimit"
)

type SourceWarning struct {
	SourceID       string
	BookSourceURL  string
	BookSourceName string
	Code           string
	Message        string
}

type MultiSearchInput struct {
	Keyword     string
	Page        int
	SourceIDs   []string
	Group       string
	EnabledOnly bool
	Precision   bool
	Concurrency int
	// Timeout 为单源搜索的超时时间；<=0 时使用 WithSourceTimeout 设置的默认值，
	// 未设置默认值时使用 built-in 15s，保证慢源不会拖住整次搜索（S01-01）。
	Timeout time.Duration
}

type MultiSearchResult struct {
	Items    []book.SearchBook
	Warnings []SourceWarning
	// All 为各源未合并的原始条目（precision 过滤后，按完成顺序排列）。
	All []book.SearchBook
	// HasMore 为本页有命中的近似语义，可以据此继续请求下一页；
	// 它不表示某个书源还有下一页（Legado 书源分页没有总数信息，空页即结束）。
	HasMore bool
}

type MultiSourceSearcher struct {
	repo          source.BookSourceRepository
	single        SourceSearcher
	limiter       *ratelimit.Limiter
	sourceTimeout time.Duration
}

// SourceSearcher 单源搜索接口，便于多源搜索与测试替换。
type SourceSearcher interface {
	Search(ctx context.Context, input SearchInput) ([]book.SearchBook, error)
}

func NewMultiSourceSearcher(repo source.BookSourceRepository, single SourceSearcher) *MultiSourceSearcher {
	return &MultiSourceSearcher{repo: repo, single: single, sourceTimeout: defaultSourceTimeout}
}

// WithSourceTimeout 设置 input.Timeout<=0 时的每源默认超时；返回自身以便链式装配。
func (s *MultiSourceSearcher) WithSourceTimeout(d time.Duration) *MultiSourceSearcher {
	if d > 0 {
		s.sourceTimeout = d
	}
	return s
}

// WithLimiter 注入限流器：多源并发度缺省取其全局并发上限，单源请求在抓取入口按源限流。
func (s *MultiSourceSearcher) WithLimiter(limiter *ratelimit.Limiter) *MultiSourceSearcher {
	s.limiter = limiter
	return s
}

// defaultSourceTimeout 为未显式指定每源超时（input.Timeout<=0 且未 WithSourceTimeout）时的兜底。
const defaultSourceTimeout = 15 * time.Second

// defaultConcurrency 为未配置任何并发参数时的兜底多源并发度。
const defaultConcurrency = 4

func (s *MultiSourceSearcher) Search(ctx context.Context, input MultiSearchInput) (MultiSearchResult, error) {
	sources, resolveWarnings, err := s.resolveSources(ctx, input)
	if err != nil {
		return MultiSearchResult{}, err
	}
	concurrency := input.Concurrency
	if concurrency < 1 {
		concurrency = s.limiter.GlobalLimit()
	}
	if concurrency < 1 {
		concurrency = defaultConcurrency
	}
	// input.Timeout<=0 时使用每源默认超时，保证慢源不会拖住整次搜索（S01-01）。
	sourceTimeout := input.Timeout
	if sourceTimeout <= 0 {
		sourceTimeout = s.sourceTimeout
		if sourceTimeout <= 0 {
			sourceTimeout = defaultSourceTimeout
		}
	}
	var mu sync.Mutex
	result := MultiSearchResult{Warnings: resolveWarnings}

	// 固定大小 worker 池：goroutine 数恒等于并发度，与源数无关（S01-02）。
	queue := make(chan *source.BookSource)
	var wg sync.WaitGroup
	workerCount := concurrency
	if workerCount > len(sources) {
		workerCount = len(sources)
	}
	// searchSource 从 queue 消费源并执行单源搜索；panic 只影响该源，转成 Warning。
	searchSource := func() {
		defer wg.Done()
		for src := range queue {
			s.runSingleSource(ctx, input, sourceTimeout, src, &mu, &result)
		}
	}
	for range workerCount {
		wg.Add(1)
		go searchSource()
	}
	for i := range sources {
		src := sources[i]
		if strings.TrimSpace(src.SearchURL) == "" {
			// 静默跳过会让用户无法知道哪些源未参与搜索，记录 Warning 与 source_unavailable 对齐（S02-07）。
			mu.Lock()
			result.Warnings = append(result.Warnings, SourceWarning{
				SourceID:       src.SourceID,
				BookSourceURL:  src.BookSourceURL,
				BookSourceName: src.BookSourceName,
				Code:           "source_skipped",
				Message:        "searchUrl is empty",
			})
			mu.Unlock()
			continue
		}
		queue <- src
	}
	close(queue)
	wg.Wait()
	result.Items = rankSearchBooks(dedupeSearchBooks(result.All), input.Keyword, input.Precision)
	result.HasMore = len(result.Items) > 0
	return result, nil
}

// runSingleSource 执行单源搜索并收敛结果与错误：panic/超时/失败均转成 Warning，不影响其余源。
func (s *MultiSourceSearcher) runSingleSource(ctx context.Context, input MultiSearchInput, sourceTimeout time.Duration, src *source.BookSource, mu *sync.Mutex, result *MultiSearchResult) {
	// 单源 panic 只影响该源，转成 Warning，其余源结果照常返回。
	defer func() {
		if recovered := recover(); recovered != nil {
			mu.Lock()
			result.Warnings = append(result.Warnings, SourceWarning{
				SourceID:       src.SourceID,
				BookSourceURL:  src.BookSourceURL,
				BookSourceName: src.BookSourceName,
				Code:           "search_panic",
				Message:        fmt.Sprintf("panic: %v", recovered),
			})
			mu.Unlock()
		}
	}()
	// 每源 timeout 在 worker 拿到任务后才创建，避免为全部源同时挂 deadline；
	// cancel 放入 defer，panic 时也会释放定时器（S02-04）。
	searchCtx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	items, err := s.single.Search(searchCtx, SearchInput{Source: src, Keyword: input.Keyword, Page: input.Page})
	mu.Lock()
	defer mu.Unlock()
	if err != nil {
		code := "search_failed"
		// 上层主动取消（如 HTTP 客户端断开）时标记 search_canceled，
		// 仅 DeadlineExceeded 或 worker 自身超时归为 search_timeout（S02-10）。
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			code = "search_timeout"
		case errors.Is(err, context.Canceled):
			if ctx.Err() != nil {
				code = "search_canceled"
			} else {
				code = "search_timeout"
			}
		}
		result.Warnings = append(result.Warnings, SourceWarning{
			SourceID:       src.SourceID,
			BookSourceURL:  src.BookSourceURL,
			BookSourceName: src.BookSourceName,
			Code:           code,
			Message:        err.Error(),
		})
		return
	}
	for _, item := range items {
		if input.Precision && !strings.Contains(item.Name, input.Keyword) && !strings.Contains(item.Author, input.Keyword) {
			continue
		}
		result.All = append(result.All, item)
	}
}

// dedupeSearchBooks 按 name+"\x00"+author 索引 O(n) 去重：同名同作者只保留一条，
// Origins 合并所有源的 Origin，其余字段取首次出现的条目。输入顺序即输出顺序。
func dedupeSearchBooks(items []book.SearchBook) []book.SearchBook {
	index := make(map[string]int, len(items))
	out := make([]book.SearchBook, 0, len(items))
	for _, item := range items {
		key := item.Name + "\x00" + item.Author
		if i, ok := index[key]; ok {
			out[i].Origins = appendOrigin(out[i].Origins, item.Origin)
			continue
		}
		index[key] = len(out)
		item.Origins = appendOrigin(item.Origins, item.Origin)
		out = append(out, item)
	}
	return out
}

// resolveSources 解析待搜索书源；单个无效 sourceId 只跳过该源并记录 Warning，不中止整个请求。
func (s *MultiSourceSearcher) resolveSources(ctx context.Context, input MultiSearchInput) ([]*source.BookSource, []SourceWarning, error) {
	if len(input.SourceIDs) > 0 {
		out := []*source.BookSource{}
		warnings := []SourceWarning{}
		for _, id := range input.SourceIDs {
			src, err := s.repo.FindByID(ctx, id)
			if err != nil {
				warnings = append(warnings, SourceWarning{SourceID: id, Code: "source_unavailable", Message: err.Error()})
				continue
			}
			if input.EnabledOnly && !src.Enabled {
				warnings = append(warnings, SourceWarning{
					SourceID:       id,
					BookSourceURL:  src.BookSourceURL,
					BookSourceName: src.BookSourceName,
					Code:           "source_disabled",
					Message:        "source is disabled",
				})
				continue
			}
			out = append(out, src)
		}
		return out, warnings, nil
	}
	filter := source.BookSourceFilter{}
	if input.Group != "" {
		filter.Group = input.Group
	}
	if input.EnabledOnly {
		enabled := true
		filter.Enabled = &enabled
	}
	items, err := source.ListAll(ctx, s.repo, filter)
	if err != nil {
		return nil, nil, err
	}
	return items, nil, nil
}

func appendOrigin(origins []string, origin string) []string {
	if origin == "" {
		return origins
	}
	for _, existing := range origins {
		if existing == origin {
			return origins
		}
	}
	return append(origins, origin)
}

// rankSearchBooks 按 等值 → 包含 → 其他 分桶排序；单次遍历分桶，
// 桶内保持输入顺序（稳定）。precision=true 时丢弃 other 桶。
func rankSearchBooks(items []book.SearchBook, keyword string, precision bool) []book.SearchBook {
	equal := make([]book.SearchBook, 0, len(items))
	contains := make([]book.SearchBook, 0)
	other := make([]book.SearchBook, 0)
	for _, item := range items {
		switch {
		case item.Name == keyword || item.Author == keyword:
			equal = append(equal, item)
		case strings.Contains(item.Name, keyword) || strings.Contains(item.Author, keyword):
			contains = append(contains, item)
		case !precision:
			other = append(other, item)
		}
	}
	out := append(equal, contains...)
	return append(out, other...)
}
