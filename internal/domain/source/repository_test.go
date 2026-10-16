package source

import (
	"context"
	"testing"
)

type pagedRepo struct {
	items []*BookSource
	calls []BookSourceFilter
}

func (r *pagedRepo) Save(ctx context.Context, source *BookSource) error { return nil }
func (r *pagedRepo) SaveMany(ctx context.Context, sources []*BookSource) error {
	return nil
}
func (r *pagedRepo) FindByID(ctx context.Context, id string) (*BookSource, error) {
	return nil, ErrNotFound
}
func (r *pagedRepo) FindByURL(ctx context.Context, url string) (*BookSource, error) {
	return nil, ErrNotFound
}
func (r *pagedRepo) List(ctx context.Context, filter BookSourceFilter) (Page[*BookSource], error) {
	r.calls = append(r.calls, filter)
	start := (filter.Page - 1) * filter.PageSize
	if start >= len(r.items) {
		return Page[*BookSource]{Total: len(r.items)}, nil
	}
	end := start + filter.PageSize
	if end > len(r.items) {
		end = len(r.items)
	}
	return Page[*BookSource]{Items: r.items[start:end], Total: len(r.items)}, nil
}
func (r *pagedRepo) Delete(ctx context.Context, id string) error { return nil }
func (r *pagedRepo) UpdateEnabled(ctx context.Context, id string, enabled bool) error {
	return nil
}
func (r *pagedRepo) UpdateExploreEnabled(ctx context.Context, id string, enabled bool) error {
	return nil
}
func (r *pagedRepo) Reorder(ctx context.Context, orders []SourceOrder) error { return nil }

func TestListAllPaginatesUntilAllSourcesFetched(t *testing.T) {
	items := make([]*BookSource, 1201)
	for i := range items {
		items[i] = &BookSource{SourceID: string(rune('a' + i%26))}
	}
	repo := &pagedRepo{items: items}

	got, err := ListAll(context.Background(), repo, BookSourceFilter{})
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	if len(got) != len(items) {
		t.Fatalf("ListAll() returned %d sources, want %d", len(got), len(items))
	}
	if len(repo.calls) != 3 {
		t.Fatalf("List called %d times, want 3", len(repo.calls))
	}
	for i, call := range repo.calls {
		if call.Page != i+1 || call.PageSize != listAllPageSize {
			t.Fatalf("call %d = %#v", i, call)
		}
	}
}

func TestListAllStopsWhenRepositoryReturnsShortPage(t *testing.T) {
	repo := &pagedRepo{items: []*BookSource{{SourceID: "a"}, {SourceID: "b"}}}

	got, err := ListAll(context.Background(), repo, BookSourceFilter{})
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	if len(got) != 2 || len(repo.calls) != 1 {
		t.Fatalf("got %d sources with %d calls", len(got), len(repo.calls))
	}
}

func TestSplitGroupsMatchesLegadoSeparators(t *testing.T) {
	got := SplitGroups("玄幻,都市;仙侠，历史；科幻")
	want := []string{"玄幻", "都市", "仙侠", "历史", "科幻"}
	if len(got) != len(want) {
		t.Fatalf("SplitGroups() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SplitGroups() = %#v, want %#v", got, want)
		}
	}

	got = SplitGroups(" 玄幻 ,, 玄幻 ; ; ")
	if len(got) != 1 || got[0] != "玄幻" {
		t.Fatalf("SplitGroups() dedup = %#v", got)
	}

	if got := SplitGroups(""); len(got) != 0 {
		t.Fatalf("SplitGroups(\"\") = %#v, want empty", got)
	}
}
