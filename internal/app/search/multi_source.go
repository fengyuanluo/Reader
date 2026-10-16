package searchapp

import (
	"context"
	"fmt"
	"strings"
	"sync"

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
}

type MultiSearchResult struct {
	Items    []book.SearchBook
	Warnings []SourceWarning
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
			items, err := s.single.Search(ctx, SearchInput{Source: src, Keyword: input.Keyword, Page: input.Page})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Warnings = append(result.Warnings, SourceWarning{
					SourceID:       src.SourceID,
					BookSourceURL:  src.BookSourceURL,
					BookSourceName: src.BookSourceName,
					Code:           "search_failed",
					Message:        err.Error(),
				})
				return
			}
			result.Items = mergeSearchBooks(result.Items, items, input.Keyword, input.Precision)
		}()
	}
	wg.Wait()
	return result, nil
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

func mergeSearchBooks(existing []book.SearchBook, incoming []book.SearchBook, keyword string, precision bool) []book.SearchBook {
	out := append([]book.SearchBook(nil), existing...)
	for _, item := range incoming {
		if precision && !strings.Contains(item.Name, keyword) && !strings.Contains(item.Author, keyword) {
			continue
		}
		merged := false
		for i := range out {
			if out[i].Name == item.Name && out[i].Author == item.Author {
				out[i].Origins = appendOrigin(out[i].Origins, out[i].Origin)
				out[i].Origins = appendOrigin(out[i].Origins, item.Origin)
				merged = true
				break
			}
		}
		if !merged {
			item.Origins = appendOrigin(item.Origins, item.Origin)
			out = append(out, item)
		}
	}
	return rankSearchBooks(out, keyword, precision)
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

func rankSearchBooks(items []book.SearchBook, keyword string, precision bool) []book.SearchBook {
	equal := []book.SearchBook{}
	contains := []book.SearchBook{}
	other := []book.SearchBook{}
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
	out = append(out, other...)
	return out
}
