package discovery

import (
	"context"
	"testing"

	"reader/internal/domain/rule"
	"reader/internal/domain/source"
)

// TestDiscoverHasMoreFollowsPageItems 覆盖 R-019：发现页 HasMore 由本页实际结果推导。
func TestDiscoverHasMoreFollowsPageItems(t *testing.T) {
	src := &source.BookSource{
		SourceID:         "s1",
		BookSourceURL:    "https://source.example.test/base/",
		BookSourceName:   "S1",
		ExploreURL:       "玄幻::/list?page={page}",
		EnabledExplore:   true,
		EnabledCookieJar: true,
		RuleExplore: rule.Flexible[rule.ExploreRule]{Value: &rule.ExploreRule{
			BookList: ".item",
			Name:     ".name@text",
			Author:   ".author@text",
		}},
		// 空页时 parseBookList 会走详情页兜底，需要一个 ruleBookInfo（否则 parseInfo 解引用空规则）。
		RuleBookInfo: rule.Flexible[rule.BookInfoRule]{Value: &rule.BookInfoRule{Name: ".detail@text"}},
	}
	repo := &fakeSourceRepo{items: []*source.BookSource{src}}

	hit := NewService(repo, fakeFetcher{
		body: `<div class="item"><span class="name">发现书</span><span class="author">作者</span></div>`,
		url:  "https://source.example.test/list?page=1",
	}, nil)
	result, err := hit.Discover(context.Background(), Input{SourceIDs: []string{"s1"}, Page: 1})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Items) != 1 || !result.HasMore {
		t.Fatalf("result = %#v, want items with HasMore", result)
	}

	empty := NewService(repo, fakeFetcher{
		body: `<div class="nothing"></div>`,
		url:  "https://source.example.test/list?page=9",
	}, nil)
	emptyResult, err := empty.Discover(context.Background(), Input{SourceIDs: []string{"s1"}, Page: 9})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(emptyResult.Items) != 0 || emptyResult.HasMore {
		t.Fatalf("result = %#v, want empty page without HasMore", emptyResult)
	}
}
