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
	err := updater.Apply(context.Background(), &task.CheckTaskItem{SourceID: "source-1", Status: task.CheckItemFailed, FailureCode: FailureContentFailed, FailureClass: diagnosticapp.FailureClassSourceContentEmpty, Message: "content empty", RespondTime: 180000})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if repo.src.RespondTime != 180000 {
		t.Fatalf("respondTime = %d", repo.src.RespondTime)
	}
	if repo.src.BookSourceGroup != "玄幻,正文失效" {
		t.Fatalf("group = %q", repo.src.BookSourceGroup)
	}
	if repo.src.BookSourceComment != "// Error: source_content_empty: content empty\n\nkeep" {
		t.Fatalf("comment = %q", repo.src.BookSourceComment)
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

func TestDiagnosticsUpdaterPreservesSourceMetadataOnNetworkFailure(t *testing.T) {
	repo := &fakeSourceRepo{src: &source.BookSource{
		SourceID:          "source-1",
		BookSourceURL:     "https://source.test",
		BookSourceName:    "Source",
		BookSourceGroup:   "玄幻,自定义",
		BookSourceComment: "keep",
	}}
	updater := NewDiagnosticsUpdater(repo)
	err := updater.Apply(context.Background(), &task.CheckTaskItem{
		SourceID:     "source-1",
		Status:       task.CheckItemFailed,
		FailureCode:  FailureSearchFailed,
		FailureClass: diagnosticapp.FailureClassNetworkTimeout,
		Message:      "context deadline exceeded",
		RespondTime:  180000,
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if repo.src.RespondTime != 180000 || repo.src.BookSourceGroup != "玄幻,自定义" || repo.src.BookSourceComment != "keep" {
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
