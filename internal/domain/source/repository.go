package source

import "context"

type Page[T any] struct {
	Items []T
	Total int
}

type BookSourceFilter struct {
	Keyword        string
	Group          string
	Enabled        *bool
	EnabledExplore *bool
	Page           int
	PageSize       int
}

type SourceOrder struct {
	SourceID    string
	CustomOrder int
}

type BookSourceRepository interface {
	Save(ctx context.Context, source *BookSource) error
	SaveMany(ctx context.Context, sources []*BookSource) error
	FindByID(ctx context.Context, id string) (*BookSource, error)
	FindByURL(ctx context.Context, url string) (*BookSource, error)
	List(ctx context.Context, filter BookSourceFilter) (Page[*BookSource], error)
	Delete(ctx context.Context, id string) error
	UpdateEnabled(ctx context.Context, id string, enabled bool) error
	UpdateExploreEnabled(ctx context.Context, id string, enabled bool) error
	Reorder(ctx context.Context, orders []SourceOrder) error
}
