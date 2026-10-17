package searchapp

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"reader/internal/domain/book"
	"reader/internal/domain/source"
)

func TestMultiSourceSearchMergesSameNameAuthorAndKeepsRawItems(t *testing.T) {
	first := []book.SearchBook{{Name: "斗破苍穹", Author: "天蚕土豆", Origin: "source-a"}}
	second := []book.SearchBook{{Name: "斗破苍穹", Author: "天蚕土豆", Origin: "source-b"}}

	repo := &fakeSourceRepo{items: []*source.BookSource{
		{SourceID: "a", BookSourceURL: "source-a", BookSourceName: "A", SearchURL: "https://a.test", Enabled: true},
		{SourceID: "b", BookSourceURL: "source-b", BookSourceName: "B", SearchURL: "https://b.test", Enabled: true},
	}}
	single := &stubSingleSearcher{byOrigin: map[string][]book.SearchBook{"source-a": first, "source-b": second}}
	searcher := NewMultiSourceSearcher(repo, single)

	result, err := searcher.Search(context.Background(), MultiSearchInput{Keyword: "斗破", EnabledOnly: true, Concurrency: 2})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %#v", result.Items)
	}
	// Origins 不保证顺序（并发完成顺序决定），只断言两个源都在且唯一。
	origins := map[string]bool{}
	for _, o := range result.Items[0].Origins {
		origins[o] = true
	}
	if len(result.Items[0].Origins) != 2 || !origins["source-a"] || !origins["source-b"] {
		t.Fatalf("origins = %#v", result.Items[0].Origins)
	}
	// All 保留每个源的原始条目（未合并）；顺序由并发完成顺序决定，只断言两个源都在。
	allOrigins := map[string]bool{}
	for _, item := range result.All {
		allOrigins[item.Origin] = true
	}
	if len(result.All) != 2 || len(allOrigins) != 2 {
		t.Fatalf("all = %#v", result.All)
	}
}

func TestMultiSourceSearchKeepsDifferentAuthorsUnmerged(t *testing.T) {
	repo := &fakeSourceRepo{items: []*source.BookSource{
		{SourceID: "a", BookSourceURL: "source-a", BookSourceName: "A", SearchURL: "https://a.test", Enabled: true},
		{SourceID: "b", BookSourceURL: "source-b", BookSourceName: "B", SearchURL: "https://b.test", Enabled: true},
	}}
	single := &stubSingleSearcher{byOrigin: map[string][]book.SearchBook{
		"source-a": {{Name: "斗破苍穹", Author: "天蚕土豆", Origin: "source-a"}},
		"source-b": {{Name: "斗破苍穹", Author: "某同人", Origin: "source-b"}},
	}}
	searcher := NewMultiSourceSearcher(repo, single)

	result, err := searcher.Search(context.Background(), MultiSearchInput{Keyword: "斗破", EnabledOnly: true, Concurrency: 2})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("同名不同作者不应合并: items = %#v", result.Items)
	}
}

func TestMultiSourceSearchPrecisionDropsNonContains(t *testing.T) {
	repo := &fakeSourceRepo{items: []*source.BookSource{
		{SourceID: "a", BookSourceURL: "source-a", BookSourceName: "A", SearchURL: "https://a.test", Enabled: true},
		{SourceID: "b", BookSourceURL: "source-b", BookSourceName: "B", SearchURL: "https://b.test", Enabled: true},
	}}
	single := &stubSingleSearcher{byOrigin: map[string][]book.SearchBook{
		"source-a": {{Name: "斗破苍穹", Author: "天蚕土豆", Origin: "source-a"}},
		"source-b": {{Name: "完全无关", Author: "另一个人", Origin: "source-b"}},
	}}
	searcher := NewMultiSourceSearcher(repo, single)

	result, err := searcher.Search(context.Background(), MultiSearchInput{Keyword: "斗破", EnabledOnly: true, Precision: true, Concurrency: 2})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Name != "斗破苍穹" {
		t.Fatalf("precision 应丢弃非包含项: items = %#v", result.Items)
	}
	if len(result.All) != 1 {
		t.Fatalf("precision 应同样过滤 All: all = %#v", result.All)
	}
}

func TestMultiSourceSearchRankingEqualThenContains(t *testing.T) {
	repo := &fakeSourceRepo{items: []*source.BookSource{
		{SourceID: "a", BookSourceURL: "source-a", BookSourceName: "A", SearchURL: "https://a.test", Enabled: true},
		{SourceID: "b", BookSourceURL: "source-b", BookSourceName: "B", SearchURL: "https://b.test", Enabled: true},
		{SourceID: "c", BookSourceURL: "source-c", BookSourceName: "C", SearchURL: "https://c.test", Enabled: true},
	}}
	single := &stubSingleSearcher{byOrigin: map[string][]book.SearchBook{
		// 故意乱序返回，验证排序仍为 等值 → 包含 → 其他。
		"source-a": {{Name: "斗破苍穹前传", Author: "天蚕土豆", Origin: "source-a"}, {Name: "其他", Author: "某人", Origin: "source-a"}},
		"source-b": {{Name: "斗破苍穹", Author: "天蚕土豆", Origin: "source-b"}},
		"source-c": {{Name: "完全无关", Author: "另一个人", Origin: "source-c"}},
	}}
	searcher := NewMultiSourceSearcher(repo, single)

	result, err := searcher.Search(context.Background(), MultiSearchInput{Keyword: "斗破苍穹", EnabledOnly: true, Concurrency: 3})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Items) != 4 {
		t.Fatalf("items = %#v", result.Items)
	}
	if result.Items[0].Name != "斗破苍穹" {
		t.Fatalf("等值应排第一: items = %#v", result.Items)
	}
	if result.Items[1].Name != "斗破苍穹前传" {
		t.Fatalf("包含应排第二: items = %#v", result.Items)
	}
	if result.Items[2].Name != "完全无关" && result.Items[3].Name != "完全无关" {
		t.Fatalf("其他桶应排在包含之后（other 两条的相对顺序由并发完成顺序决定）: items = %#v", result.Items)
	}
	if result.Items[0].Name == "完全无关" || result.Items[0].Name == "其他" {
		t.Fatalf("其他不应排第一: items = %#v", result.Items)
	}
}

func TestMultiSourceSearchTimeoutMarksWarning(t *testing.T) {
	repo := &fakeSourceRepo{items: []*source.BookSource{
		{SourceID: "slow", BookSourceURL: "source-slow", BookSourceName: "S", SearchURL: "https://s.test", Enabled: true},
	}}
	single := slowSingleSearcher{}
	searcher := NewMultiSourceSearcher(repo, single)

	result, err := searcher.Search(context.Background(), MultiSearchInput{Keyword: "斗破", EnabledOnly: true, Concurrency: 1, Timeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("items = %#v", result.Items)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != "search_timeout" {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

type stubSingleSearcher struct {
	byOrigin map[string][]book.SearchBook
}

func (s *stubSingleSearcher) Search(ctx context.Context, input SearchInput) ([]book.SearchBook, error) {
	return s.byOrigin[input.Source.BookSourceURL], nil
}

type slowSingleSearcher struct{}

func (slowSingleSearcher) Search(ctx context.Context, input SearchInput) ([]book.SearchBook, error) {
	select {
	case <-time.After(2 * time.Second):
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestMultiSourceSearchSkipsDisabledByDefaultFilter(t *testing.T) {
	repo := &fakeSourceRepo{items: []*source.BookSource{
		{SourceID: "enabled", BookSourceURL: "source-a", BookSourceName: "A", SearchURL: "https://a.test", Enabled: true},
		{SourceID: "disabled", BookSourceURL: "source-b", BookSourceName: "B", SearchURL: "https://b.test", Enabled: false},
	}}
	single := &SingleSourceSearcher{}
	searcher := NewMultiSourceSearcher(repo, single)

	sources, warnings, err := searcher.resolveSources(context.Background(), MultiSearchInput{EnabledOnly: true})
	if err != nil {
		t.Fatalf("resolveSources() error = %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if len(sources) != 1 || sources[0].SourceID != "enabled" {
		t.Fatalf("sources = %#v", sources)
	}
}

func TestMultiSourceSearchConvertsSourceFailureToWarning(t *testing.T) {
	repo := &fakeSourceRepo{items: []*source.BookSource{
		{SourceID: "ok", BookSourceURL: "source-a", BookSourceName: "A", SearchURL: "https://a.test", Enabled: true},
		{SourceID: "bad", BookSourceURL: "source-b", BookSourceName: "B", SearchURL: "https://b.test", Enabled: true},
	}}
	single := fakeSingleSearcher{failSourceID: "bad"}
	searcher := NewMultiSourceSearcher(repo, single)

	result, err := searcher.Search(context.Background(), MultiSearchInput{Keyword: "斗破", EnabledOnly: true, Concurrency: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Items) != 1 || len(result.Warnings) != 1 || result.Warnings[0].SourceID != "bad" {
		t.Fatalf("result = %#v", result)
	}
}

func TestMultiSourceSearchSkipsInvalidSourceIDWithWarning(t *testing.T) {
	repo := &fakeSourceRepo{items: []*source.BookSource{
		{SourceID: "ok", BookSourceURL: "source-a", BookSourceName: "A", SearchURL: "https://a.test", Enabled: true},
	}}
	searcher := NewMultiSourceSearcher(repo, fakeSingleSearcher{})

	result, err := searcher.Search(context.Background(), MultiSearchInput{Keyword: "斗破", SourceIDs: []string{"missing", "ok"}, Concurrency: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %#v, want one result from the valid source", result.Items)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].SourceID != "missing" || result.Warnings[0].Code != "source_unavailable" {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestMultiSourceSearchRecoversFromSourcePanic(t *testing.T) {
	repo := &fakeSourceRepo{items: []*source.BookSource{
		{SourceID: "bad", BookSourceURL: "source-bad", BookSourceName: "Bad", SearchURL: "https://bad.test", Enabled: true},
		{SourceID: "ok", BookSourceURL: "source-ok", BookSourceName: "Ok", SearchURL: "https://ok.test", Enabled: true},
	}}
	searcher := NewMultiSourceSearcher(repo, panicSingleSearcher{})

	result, err := searcher.Search(context.Background(), MultiSearchInput{Keyword: "斗破", EnabledOnly: true, Concurrency: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Origin != "source-ok" {
		t.Fatalf("items = %#v", result.Items)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].SourceID != "bad" || result.Warnings[0].Code != "search_panic" {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

type panicSingleSearcher struct{}

func (panicSingleSearcher) Search(ctx context.Context, input SearchInput) ([]book.SearchBook, error) {
	if input.Source.SourceID == "bad" {
		panic("boom")
	}
	return []book.SearchBook{{Name: "斗破苍穹", Author: "天蚕土豆", Origin: input.Source.BookSourceURL}}, nil
}

func TestMultiSourceSearchPaginatesSourceList(t *testing.T) {
	items := make([]*source.BookSource, 1201)
	for i := range items {
		items[i] = &source.BookSource{
			SourceID:       fmt.Sprintf("s%d", i),
			BookSourceURL:  fmt.Sprintf("https://s%d.test", i),
			BookSourceName: fmt.Sprintf("S%d", i),
			SearchURL:      fmt.Sprintf("https://s%d.test/search", i),
			Enabled:        true,
		}
	}
	repo := &pagedSourceRepo{items: items}
	searcher := NewMultiSourceSearcher(repo, fakeSingleSearcher{})

	sources, warnings, err := searcher.resolveSources(context.Background(), MultiSearchInput{EnabledOnly: true})
	if err != nil {
		t.Fatalf("resolveSources() error = %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if len(sources) != len(items) {
		t.Fatalf("sources = %d, want %d", len(sources), len(items))
	}
	if repo.pages != 3 {
		t.Fatalf("List calls = %d, want 3", repo.pages)
	}
}

// pagedSourceRepo 按 Page/PageSize 真实分页，用于验证全量拉取不会静默截断。
type pagedSourceRepo struct {
	items []*source.BookSource
	pages int
}

func (r *pagedSourceRepo) Save(ctx context.Context, source *source.BookSource) error { return nil }
func (r *pagedSourceRepo) SaveMany(ctx context.Context, sources []*source.BookSource) error {
	return nil
}
func (r *pagedSourceRepo) FindByID(ctx context.Context, id string) (*source.BookSource, error) {
	return nil, source.ErrNotFound
}
func (r *pagedSourceRepo) FindByURL(ctx context.Context, url string) (*source.BookSource, error) {
	return nil, source.ErrNotFound
}
func (r *pagedSourceRepo) List(ctx context.Context, filter source.BookSourceFilter) (source.Page[*source.BookSource], error) {
	r.pages++
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = len(r.items)
	}
	start := (filter.Page - 1) * pageSize
	if start >= len(r.items) {
		return source.Page[*source.BookSource]{Total: len(r.items)}, nil
	}
	end := start + pageSize
	if end > len(r.items) {
		end = len(r.items)
	}
	return source.Page[*source.BookSource]{Items: r.items[start:end], Total: len(r.items)}, nil
}
func (r *pagedSourceRepo) Delete(ctx context.Context, id string) error { return nil }
func (r *pagedSourceRepo) UpdateEnabled(ctx context.Context, id string, enabled bool) error {
	return nil
}
func (r *pagedSourceRepo) UpdateExploreEnabled(ctx context.Context, id string, enabled bool) error {
	return nil
}
func (r *pagedSourceRepo) Reorder(ctx context.Context, orders []source.SourceOrder) error { return nil }

type fakeSingleSearcher struct {
	failSourceID string
}

func (s fakeSingleSearcher) Search(ctx context.Context, input SearchInput) ([]book.SearchBook, error) {
	if input.Source.SourceID == s.failSourceID {
		return nil, errors.New("boom")
	}
	return []book.SearchBook{{Name: "斗破苍穹", Author: "天蚕土豆", Origin: input.Source.BookSourceURL}}, nil
}

type fakeSourceRepo struct {
	items []*source.BookSource
}

func (r *fakeSourceRepo) Save(ctx context.Context, source *source.BookSource) error { return nil }
func (r *fakeSourceRepo) SaveMany(ctx context.Context, sources []*source.BookSource) error {
	return nil
}
func (r *fakeSourceRepo) FindByID(ctx context.Context, id string) (*source.BookSource, error) {
	for _, item := range r.items {
		if item.SourceID == id {
			return item, nil
		}
	}
	return nil, source.ErrNotFound
}
func (r *fakeSourceRepo) FindByURL(ctx context.Context, url string) (*source.BookSource, error) {
	return nil, source.ErrNotFound
}
func (r *fakeSourceRepo) List(ctx context.Context, filter source.BookSourceFilter) (source.Page[*source.BookSource], error) {
	if filter.Enabled != nil && *filter.Enabled {
		out := []*source.BookSource{}
		for _, item := range r.items {
			if item.Enabled {
				out = append(out, item)
			}
		}
		return source.Page[*source.BookSource]{Items: out, Total: len(out)}, nil
	}
	return source.Page[*source.BookSource]{Items: r.items, Total: len(r.items)}, nil
}
func (r *fakeSourceRepo) Delete(ctx context.Context, id string) error { return nil }
func (r *fakeSourceRepo) UpdateEnabled(ctx context.Context, id string, enabled bool) error {
	return errors.New("not implemented")
}
func (r *fakeSourceRepo) UpdateExploreEnabled(ctx context.Context, id string, enabled bool) error {
	return errors.New("not implemented")
}
func (r *fakeSourceRepo) Reorder(ctx context.Context, orders []source.SourceOrder) error { return nil }
