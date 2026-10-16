package checkapp

import (
	"context"
	"testing"

	diagnosticapp "reader/internal/app/diagnostic"
	"reader/internal/domain/source"
	"reader/internal/domain/task"
)

func TestDiagnosticsUpdaterMarksFailedSource(t *testing.T) {
	repo := &fakeSourceRepo{src: &source.BookSource{SourceID: "source-1", BookSourceURL: "https://source.test", BookSourceName: "Source", BookSourceGroup: "玄幻,搜索失效", BookSourceComment: "// Error: old\n\nkeep"}}
	updater := NewDiagnosticsUpdater(repo)
	err := updater.Apply(context.Background(), &task.CheckTaskItem{SourceID: "source-1", Status: task.CheckItemFailed, FailureCode: FailureContentFailed, FailureClass: diagnosticapp.FailureClassSourceContentEmpty, Message: "content empty", Path: task.CheckPathSearch, RespondTime: 180000})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if repo.src.RespondTime != 180000 {
		t.Fatalf("respondTime = %d", repo.src.RespondTime)
	}
	// Legado CheckSourceService 用“搜索正文失效”区分搜索链路与发现链路的正文失败。
	if repo.src.BookSourceGroup != "玄幻,搜索正文失效" {
		t.Fatalf("group = %q", repo.src.BookSourceGroup)
	}
	if repo.src.BookSourceComment != "// Error: source_content_empty: content empty\n\nkeep" {
		t.Fatalf("comment = %q", repo.src.BookSourceComment)
	}
}

func TestGroupForFailureMatchesLegadoGroups(t *testing.T) {
	cases := []struct {
		code  string
		path  task.CheckItemPath
		group string
	}{
		{FailureTimeout, task.CheckPathSearch, "校验超时"},
		{FailureJSFailed, task.CheckPathSearch, "js失效"},
		{FailureSearchFailed, task.CheckPathSearch, "搜索失效"},
		{FailureDiscoveryFailed, task.CheckPathDiscovery, "发现失效"},
		{FailureTocFailed, task.CheckPathSearch, "搜索目录失效"},
		{FailureTocFailed, task.CheckPathDiscovery, "发现目录失效"},
		{FailureContentFailed, task.CheckPathSearch, "搜索正文失效"},
		{FailureContentFailed, task.CheckPathDiscovery, "发现正文失效"},
		{FailureSearchRuleEmpty, "", "搜索链接规则为空"},
		{FailureDiscoveryRuleEmpty, "", "发现规则为空"},
		{FailureWebsiteFailed, task.CheckPathSearch, "网站失效"},
	}
	for _, tc := range cases {
		if got := groupForFailure(tc.code, tc.path); got != tc.group {
			t.Fatalf("groupForFailure(%q, %q) = %q, want %q", tc.code, tc.path, got, tc.group)
		}
	}
}

func TestDiagnosticsUpdaterClearsRuleEmptyGroupsOnPass(t *testing.T) {
	repo := &fakeSourceRepo{src: &source.BookSource{SourceID: "source-1", BookSourceURL: "https://source.test", BookSourceName: "Source", BookSourceGroup: "玄幻,搜索链接规则为空,发现规则为空"}}
	updater := NewDiagnosticsUpdater(repo)
	if err := updater.Apply(context.Background(), &task.CheckTaskItem{SourceID: "source-1", Status: task.CheckItemPassed}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if repo.src.BookSourceGroup != "玄幻" {
		t.Fatalf("group = %q", repo.src.BookSourceGroup)
	}
}

func TestDiagnosticsUpdaterUsesPlainMessageWhenFailureClassMissing(t *testing.T) {
	repo := &fakeSourceRepo{src: &source.BookSource{SourceID: "source-1", BookSourceURL: "https://source.test", BookSourceName: "Source", BookSourceGroup: "玄幻", BookSourceComment: "keep"}}
	updater := NewDiagnosticsUpdater(repo)
	err := updater.Apply(context.Background(), &task.CheckTaskItem{SourceID: "source-1", Status: task.CheckItemFailed, FailureCode: FailureWebsiteFailed, Message: "plain failure", RespondTime: 42})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if repo.src.BookSourceComment != "// Error: plain failure\n\nkeep" {
		t.Fatalf("comment = %q", repo.src.BookSourceComment)
	}
}

func TestDiagnosticsUpdaterClearsFailureGroupsOnPass(t *testing.T) {
	repo := &fakeSourceRepo{src: &source.BookSource{SourceID: "source-1", BookSourceURL: "https://source.test", BookSourceName: "Source", BookSourceGroup: "玄幻,正文失效,校验超时", BookSourceComment: "// Error: old\n\nkeep"}}
	updater := NewDiagnosticsUpdater(repo)
	err := updater.Apply(context.Background(), &task.CheckTaskItem{SourceID: "source-1", Status: task.CheckItemPassed, RespondTime: 12})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if repo.src.BookSourceGroup != "玄幻" || repo.src.BookSourceComment != "keep" || repo.src.RespondTime != 12 {
		t.Fatalf("source = %#v", repo.src)
	}
}

type fakeSourceRepo struct {
	src *source.BookSource
}

func (r *fakeSourceRepo) FindByID(ctx context.Context, id string) (*source.BookSource, error) {
	copy := *r.src
	return &copy, nil
}

func (r *fakeSourceRepo) Save(ctx context.Context, src *source.BookSource) error {
	copy := *src
	r.src = &copy
	return nil
}
