package source

import (
	"context"
	"strings"
)

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

// SplitGroups 拆分书源分组字符串，对齐 Legado AppPattern.splitGroupRegex（[,;，；]）与
// splitNotBlank：按英文/中文逗号与分号切分，去空白、去空项并去重。
func SplitGroups(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '，' || r == '；'
	})
	out := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, field := range fields {
		group := strings.TrimSpace(field)
		if group == "" {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		out = append(out, group)
	}
	return out
}

// listAllPageSize 为全量拉取书源时的分页大小。
const listAllPageSize = 500

// ListAll 分页拉取满足 filter 的全部书源，避免调用方固定 PageSize 造成静默截断。
func ListAll(ctx context.Context, repo BookSourceRepository, filter BookSourceFilter) ([]*BookSource, error) {
	out := []*BookSource{}
	for page := 1; ; page++ {
		filter.Page = page
		filter.PageSize = listAllPageSize
		result, err := repo.List(ctx, filter)
		if err != nil {
			return nil, err
		}
		out = append(out, result.Items...)
		if len(result.Items) < listAllPageSize || len(out) >= result.Total {
			break
		}
	}
	return out, nil
}
