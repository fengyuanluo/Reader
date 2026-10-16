package searchapp

import (
	"context"
	"testing"

	"reader/internal/domain/book"
	"reader/internal/domain/source"
)

type emptySingleSearcher struct{}

func (emptySingleSearcher) Search(context.Context, SearchInput) ([]book.SearchBook, error) {
	return nil, nil
}

// TestMultiSourceSearcherHasMoreFollowsPageItems 覆盖 R-019：HasMore 由本页实际结果推导，而不是写死。
func TestMultiSourceSearcherHasMoreFollowsPageItems(t *testing.T) {
	repo := &fakeSourceRepo{items: []*source.BookSource{
		{SourceID: "s1", BookSourceURL: "source-a", BookSourceName: "A", SearchURL: "https://a.test", Enabled: true},
	}}

	hit := NewMultiSourceSearcher(repo, fakeSingleSearcher{})
	result, err := hit.Search(context.Background(), MultiSearchInput{Keyword: "斗破", EnabledOnly: true, Concurrency: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Items) == 0 || !result.HasMore {
		t.Fatalf("result = %#v, want items with HasMore", result)
	}

	empty := NewMultiSourceSearcher(repo, emptySingleSearcher{})
	emptyResult, err := empty.Search(context.Background(), MultiSearchInput{Keyword: "斗破", EnabledOnly: true, Concurrency: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(emptyResult.Items) != 0 || emptyResult.HasMore {
		t.Fatalf("result = %#v, want empty page without HasMore", emptyResult)
	}
}
