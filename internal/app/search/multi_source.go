package searchapp

import (
	"context"
	"strings"
	"sync"

	"reader/internal/domain/book"
	"reader/internal/domain/source"
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
	repo   source.BookSourceRepository
	single SourceSearcher
}

type SourceSearcher interface {
	Search(ctx context.Context, input SearchInput) ([]book.SearchBook, error)
}

func NewMultiSourceSearcher(repo source.BookSourceRepository, single SourceSearcher) *MultiSourceSearcher {
	return &MultiSourceSearcher{repo: repo, single: single}
}

func (s *MultiSourceSearcher) Search(ctx context.Context, input MultiSearchInput) (MultiSearchResult, error) {
	sources, err := s.resolveSources(ctx, input)
	if err != nil {
		return MultiSearchResult{}, err
	}
	concurrency := input.Concurrency
	if concurrency < 1 {
		concurrency = 4
	}
	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex
	result := MultiSearchResult{}
	var wg sync.WaitGroup
	for _, src := range sources {
		src := src
		if strings.TrimSpace(src.SearchURL) == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
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

func (s *MultiSourceSearcher) resolveSources(ctx context.Context, input MultiSearchInput) ([]*source.BookSource, error) {
	if len(input.SourceIDs) > 0 {
		out := []*source.BookSource{}
		for _, id := range input.SourceIDs {
			src, err := s.repo.FindByID(ctx, id)
			if err != nil {
				return nil, err
			}
			if input.EnabledOnly && !src.Enabled {
				continue
			}
			out = append(out, src)
		}
		return out, nil
	}
	filter := source.BookSourceFilter{Page: 1, PageSize: 10000}
	if input.Group != "" {
		filter.Group = input.Group
	}
	if input.EnabledOnly {
		enabled := true
		filter.Enabled = &enabled
	}
	page, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
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
