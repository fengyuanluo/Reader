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
	// Timeout 为单源搜索的超时时间；<=0 表示不启用（保持原有行为）。
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
	repo    source.BookSourceRepository
	single  SourceSearcher
	limiter *ratelimit.Limiter
}

// SourceSearcher 单源搜索接口，便于多源搜索与测试替换。
type SourceSearcher interface {
	Search(ctx context.Context, input SearchInput) ([]book.SearchBook, error)
}

func NewMultiSourceSearcher(repo source.BookSourceRepository, single SourceSearcher) *MultiSourceSearcher {
	return &MultiSourceSearcher{repo: repo, single: single}
}

// WithLimiter 注入限流器：多源并发度缺省取其全局并发上限，单源请求在抓取入口按源限流。
func (s *MultiSourceSearcher) WithLimiter(limiter *ratelimit.Limiter) *MultiSourceSearcher {
	s.limiter = limiter
	return s
}

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
	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex
	result := MultiSearchResult{Warnings: resolveWarnings}
	var wg sync.WaitGroup
	for _, src := range sources {
		src := src
		if strings.TrimSpace(src.SearchURL) == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
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
			sem <- struct{}{}
			defer func() { <-sem }()
			searchCtx := ctx
			cancel := func() {}
			if input.Timeout > 0 {
				searchCtx, cancel = context.WithTimeout(ctx, input.Timeout)
			}
			items, err := s.single.Search(searchCtx, SearchInput{Source: src, Keyword: input.Keyword, Page: input.Page})
			cancel()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				code := "search_failed"
				if input.Timeout > 0 && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
					code = "search_timeout"
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
			// 锁内只 append 原始条目，去重与排序在 wg.Wait() 之后统一做一次。
			for _, item := range items {
				if input.Precision && !strings.Contains(item.Name, input.Keyword) && !strings.Contains(item.Author, input.Keyword) {
					continue
				}
				result.All = append(result.All, item)
			}
		}()
	}
	wg.Wait()
	result.Items = rankSearchBooks(dedupeSearchBooks(result.All), input.Keyword, input.Precision)
	result.HasMore = len(result.Items) > 0
	return result, nil
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
