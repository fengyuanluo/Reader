package searchapp

import (
	"context"
	"errors"
	"testing"

	"reader/internal/domain/book"
	"reader/internal/domain/source"
)

func TestMergeSearchBooksMergesSameNameAuthorOrigins(t *testing.T) {
	first := []book.SearchBook{{Name: "斗破苍穹", Author: "天蚕土豆", Origin: "source-a"}}
	second := []book.SearchBook{{Name: "斗破苍穹", Author: "天蚕土豆", Origin: "source-b"}}

	got := mergeSearchBooks(first, second, "斗破", false)
	if len(got) != 1 {
		t.Fatalf("got = %#v", got)
	}
	if len(got[0].Origins) != 2 || got[0].Origins[0] != "source-a" || got[0].Origins[1] != "source-b" {
		t.Fatalf("origins = %#v", got[0].Origins)
	}
}

func TestMultiSourceSearchSkipsDisabledByDefaultFilter(t *testing.T) {
	repo := &fakeSourceRepo{items: []*source.BookSource{
		{SourceID: "enabled", BookSourceURL: "source-a", BookSourceName: "A", SearchURL: "https://a.test", Enabled: true},
		{SourceID: "disabled", BookSourceURL: "source-b", BookSourceName: "B", SearchURL: "https://b.test", Enabled: false},
	}}
	single := &SingleSourceSearcher{}
	searcher := NewMultiSourceSearcher(repo, single)

	sources, err := searcher.resolveSources(context.Background(), MultiSearchInput{EnabledOnly: true})
	if err != nil {
		t.Fatalf("resolveSources() error = %v", err)
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
