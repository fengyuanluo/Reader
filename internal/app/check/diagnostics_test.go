package checkapp

import (
	"context"
	"sync"
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
	if repo.respondTime() != 180000 {
		t.Fatalf("respondTime = %d", repo.respondTime())
	}
	// Legado CheckSourceService 用“搜索正文失效”区分搜索链路与发现链路的正文失败。
	if repo.group() != "玄幻,搜索正文失效" {
		t.Fatalf("group = %q", repo.group())
	}
	if repo.comment() != "// Error: source_content_empty: content empty\n\nkeep" {
		t.Fatalf("comment = %q", repo.comment())
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
	if repo.group() != "玄幻" {
		t.Fatalf("group = %q", repo.group())
	}
}

func TestDiagnosticsUpdaterUsesPlainMessageWhenFailureClassMissing(t *testing.T) {
	repo := &fakeSourceRepo{src: &source.BookSource{SourceID: "source-1", BookSourceURL: "https://source.test", BookSourceName: "Source", BookSourceGroup: "玄幻", BookSourceComment: "keep"}}
	updater := NewDiagnosticsUpdater(repo)
	err := updater.Apply(context.Background(), &task.CheckTaskItem{SourceID: "source-1", Status: task.CheckItemFailed, FailureCode: FailureWebsiteFailed, Message: "plain failure", RespondTime: 42})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if repo.comment() != "// Error: plain failure\n\nkeep" {
		t.Fatalf("comment = %q", repo.comment())
	}
}

func TestDiagnosticsUpdaterClearsFailureGroupsOnPass(t *testing.T) {
	repo := &fakeSourceRepo{src: &source.BookSource{SourceID: "source-1", BookSourceURL: "https://source.test", BookSourceName: "Source", BookSourceGroup: "玄幻,正文失效,校验超时", BookSourceComment: "// Error: old\n\nkeep"}}
	updater := NewDiagnosticsUpdater(repo)
	err := updater.Apply(context.Background(), &task.CheckTaskItem{SourceID: "source-1", Status: task.CheckItemPassed, RespondTime: 12})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if repo.group() != "玄幻" || repo.comment() != "keep" || repo.respondTime() != 12 {
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
	if repo.respondTime() != 180000 || repo.group() != "玄幻,自定义" || repo.comment() != "keep" {
		t.Fatalf("source = %#v", repo.src)
	}
}

type fakeSourceRepo struct {
	mu      sync.Mutex
	src     *source.BookSource
	respond int64
}

// UpdateSourceDiagnostics 模拟窄列更新：transform 在当前 group/comment 上求值，
// 只记录三列写入，模拟 SQLite CheckRepository 的窄列语义。
func (r *fakeSourceRepo) UpdateSourceDiagnostics(ctx context.Context, sourceID string, respondTime int64, transform func(group, comment string) (string, string)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	newGroup, newComment := transform(r.src.BookSourceGroup, r.src.BookSourceComment)
	r.src.RespondTime = respondTime
	r.src.BookSourceGroup = newGroup
	r.src.BookSourceComment = newComment
	r.respond = respondTime
	return nil
}

// group 断言用：读取最近一次窄列写入的分组。
func (r *fakeSourceRepo) group() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.src.BookSourceGroup
}

// comment 断言用：读取窄列写入后的注释。
func (r *fakeSourceRepo) comment() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.src.BookSourceComment
}

// respondTime 断言用：读取窄列写入后的响应时间。
func (r *fakeSourceRepo) respondTime() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.respond
}

// TestDiagnosticsUpdaterUsesNarrowColumnUpdate 覆盖 S03-02/S03-03：支持窄列更新的仓储
// 走窄列路径，只写 respond_time/分组/注释，不整行覆盖并发修改的其他列。
func TestDiagnosticsUpdaterUsesNarrowColumnUpdate(t *testing.T) {
	repo := &fakeSourceRepo{src: &source.BookSource{SourceID: "source-1", BookSourceURL: "https://source.test", BookSourceName: "用户改过的名字", BookSourceGroup: "玄幻"}}
	updater := NewDiagnosticsUpdater(repo)
	err := updater.Apply(context.Background(), &task.CheckTaskItem{SourceID: "source-1", Status: task.CheckItemPassed, RespondTime: 250})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if repo.group() != "玄幻" {
		t.Fatalf("group = %q, want unchanged", repo.group())
	}
	if repo.respondTime() != 250 {
		t.Fatalf("respondTime = %d, want 250 (narrow column write)", repo.respondTime())
	}
	if repo.src.BookSourceName != "用户改过的名字" {
		t.Fatalf("name = %q, want untouched by the diagnostic write", repo.src.BookSourceName)
	}
}

// TestDiagnosticsUpdaterSerializesConcurrentApplies 覆盖 S03-03：两个 goroutine 并发 Apply 同一源，
// 读改写在同一把锁内完成，最终分组是两次写入中后完成的那个，不会交错产生损坏的分组串。
func TestDiagnosticsUpdaterSerializesConcurrentApplies(t *testing.T) {
	repo := &fakeSourceRepo{src: &source.BookSource{SourceID: "source-1", BookSourceURL: "https://source.test", BookSourceName: "Source", BookSourceGroup: "玄幻"}}
	updater := NewDiagnosticsUpdater(repo)
	var wg sync.WaitGroup
	for i := range 2 {
		item := &task.CheckTaskItem{SourceID: "source-1", Status: task.CheckItemFailed, FailureCode: FailureSearchFailed, FailureClass: diagnosticapp.FailureClassSourceSearchEmptyResult, Message: "empty", RespondTime: int64(100 + i)}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := updater.Apply(context.Background(), item); err != nil {
				t.Errorf("Apply() error = %v", err)
			}
		}()
	}
	wg.Wait()
	if got := repo.group(); got != "玄幻,搜索失效" {
		t.Fatalf("group = %q, want a complete single write of 玄幻,搜索失效", got)
	}
}
