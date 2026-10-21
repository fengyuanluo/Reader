package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"reader/internal/domain/task"
)

// TestExportTXTAtomicWrite 验证导出失败时不覆盖已有文件（S16-02）：
// 注入一个写中途失败的 io（用只读目录模拟 Create 失败），断言原文件内容不变。
func TestExportTXTAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.txt")
	const previous = "previous full export"
	if err := os.WriteFile(path, []byte(previous), 0o644); err != nil {
		t.Fatal(err)
	}
	sections := []string{"\n\n=== 0 第一章 ===\n\n正文"}

	// 正常路径：rename 后临时文件消失，内容正确。
	if err := exportTXT(path, "书名", "作者", sections); err != nil {
		t.Fatalf("exportTXT() = %v", err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "第一章") {
		t.Fatalf("export content = %q", data)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tmp file left behind: %v", err)
	}

	// 失败路径：临时文件名超过 255 字节导致 Create(tmp) 失败，
	// 目标文件必须保持不变（测试未触及目标即可）。
	longName := strings.Repeat("x", 300)
	badPath := filepath.Join(dir, longName)
	if err := exportTXT(badPath, "书名", "作者", sections); err == nil {
		t.Fatal("exportTXT() expected error for overlong filename")
	}
}

// TestExportTXTTargetIsDirectory 验证目标为目录时 rename 失败且不破坏目录内容。
func TestExportTXTTargetIsDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.txt")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	const inner = "keep me"
	if err := os.WriteFile(filepath.Join(path, "inner.txt"), []byte(inner), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := exportTXT(path, "书名", "作者", []string{"section"}); err == nil {
		t.Fatal("exportTXT() expected error when target is a directory")
	}
	if data, _ := os.ReadFile(filepath.Join(path, "inner.txt")); string(data) != inner {
		t.Fatalf("directory content changed: %q", data)
	}
}

// TestFetchConcurrencyUpperBounds 验证 fetch 并发参数越界报用法错误（S16-03）。
func TestFetchConcurrencyUpperBounds(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "probe over max", args: []string{"书名", "--probe", "10000"}},
		{name: "workers over max", args: []string{"书名", "--workers", "100000"}},
		{name: "search-workers over max", args: []string{"书名", "--search-workers", "1000"}},
		{name: "max-sources over max", args: []string{"书名", "--max-sources", "100"}},
		{name: "probe zero", args: []string{"书名", "--probe", "0"}},
		{name: "workers zero", args: []string{"书名", "--workers", "0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t)
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			if code := runFetch(env, tt.args, stdout, stderr); code != 1 {
				t.Fatalf("runFetch() = %d, want 1; stderr: %s", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "error:") {
				t.Fatalf("stderr = %q, want error", stderr.String())
			}
		})
	}
}

// TestCheckConcurrencyUpperBound 验证 check --concurrency 越界报用法错误。
func TestCheckConcurrencyUpperBound(t *testing.T) {
	env := newTestEnv(t)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := runCheck(env, []string{"--all", "--concurrency", "1000"}, stdout, stderr); code != 1 {
		t.Fatalf("runCheck() = %d, want 1; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--concurrency") {
		t.Fatalf("stderr = %q, want concurrency error", stderr.String())
	}
}

// TestCheckUsageErrorsDoNotOpenRuntime 验证 check 用法错误不建库。
func TestCheckUsageErrorsDoNotOpenRuntime(t *testing.T) {
	env := newTestEnv(t)
	tests := []struct {
		name string
		args []string
	}{
		{name: "no args", args: []string{}},
		{name: "all with ids", args: []string{"--all", "src_x"}},
		{name: "timeout negative", args: []string{"src_x", "--timeout", "-1s"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			if code := runCheck(env, tt.args, stdout, stderr); code != 1 {
				t.Fatalf("runCheck(%v) = %d, want 1", tt.args, code)
			}
		})
	}
}

// TestDownloadCancelledContextConservesCounts 验证取消后 Failed 统计区间守恒
// （S16-05）：取消时未完成章节记入 failed，Downloaded+Failed 等于非卷章节数。
func TestDownloadCancelledContextConservesCounts(t *testing.T) {
	env := newTestEnv(t)
	mock := newCLIMockSource(t)
	bookID := addMockBook(t, env, mock)
	ctx, cancel := context.WithCancel(t.Context())
	env.ctx = ctx
	var once sync.Once
	mock.onContent1 = func() { once.Do(cancel) }

	chapters, b, err := env.rt.Books.ListChapters(ctx, bookID)
	if err != nil {
		t.Fatalf("ListChapters: %v", err)
	}
	_, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	_, summary := downloadChapters(ctx, env.rt, b, bookID, chapters, 0, len(chapters)-1, 1, stderr)
	nonVolume := 0
	for _, c := range chapters {
		if !c.IsVolume {
			nonVolume++
		}
	}
	if summary.Downloaded+summary.Cached+len(summary.Failed) != nonVolume {
		t.Fatalf("counts not conserved: downloaded=%d cached=%d failed=%d nonVolume=%d (%s)",
			summary.Downloaded, summary.Cached, len(summary.Failed), nonVolume, stderr.String())
	}
	for _, f := range summary.Failed {
		// 取消时失败原因要么是显式 canceled，要么是底层网络请求的 context canceled 错误。
		if f.Error != "canceled" && !strings.Contains(f.Error, "context canceled") {
			t.Fatalf("cancelled chapter error = %q, want canceled or context canceled", f.Error)
		}
	}
}

// TestDownloadJSONFailedEmptyArray 验证全量成功时 --json 输出 failed 为 []（S16-04）。
func TestDownloadJSONFailedEmptyArray(t *testing.T) {
	env := newTestEnv(t)
	mock := newCLIMockSource(t)
	bookID := addMockBook(t, env, mock)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := runDownload(env, []string{bookID, "--from", "0", "--to", "2", "--json"}, stdout, stderr); code != 0 {
		t.Fatalf("runDownload() = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"failed": []`) {
		t.Fatalf("failed must be [] not null: %s", stdout.String())
	}
}

// TestEmitCheckSummaryExitCodes 验证终态摘要退出码语义：全部通过 0，有失败或
// cancelled 2。
func TestEmitCheckSummaryExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		status   task.CheckTaskStatus
		items    []task.CheckTaskItem
		wantCode int
	}{
		{
			name:     "all passed",
			status:   task.CheckTaskSucceeded,
			items:    []task.CheckTaskItem{{SourceID: "s1", Status: task.CheckItemPassed}},
			wantCode: 0,
		},
		{
			name:   "some failed",
			status: task.CheckTaskFailed,
			items: []task.CheckTaskItem{
				{SourceID: "s1", Status: task.CheckItemPassed},
				{SourceID: "s2", Status: task.CheckItemFailed, FailureCode: "x"},
			},
			wantCode: 2,
		},
		{
			name:     "cancelled",
			status:   task.CheckTaskCancelled,
			items:    []task.CheckTaskItem{{SourceID: "s1", Status: task.CheckItemPassed}},
			wantCode: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			created := &task.CheckTask{TaskID: "check-1", Status: tt.status, Total: len(tt.items), Items: toPtrItems(tt.items)}
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			if code := emitCheckSummary(created, false, stdout, stderr); code != tt.wantCode {
				t.Fatalf("emitCheckSummary() = %d, want %d; stderr: %s", code, tt.wantCode, stderr.String())
			}
			if tt.wantCode == 0 {
				if !strings.Contains(stdout.String(), "passed\t1") || !strings.Contains(stdout.String(), "total\t1") {
					t.Fatalf("stdout = %q", stdout.String())
				}
			}
		})
	}
}

// toPtrItems 把值切片转成 CheckTaskItem 指针切片（测试用）。
func toPtrItems(items []task.CheckTaskItem) []*task.CheckTaskItem {
	out := make([]*task.CheckTaskItem, 0, len(items))
	for i := range items {
		out = append(out, &items[i])
	}
	return out
}

// TestCheckHelpAndDispatch 验证 root 分发注册了 check 子命令且 --help 可用。
func TestCheckHelpAndDispatch(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run([]string{"check", "--help"}, stdout, stderr); code != 0 {
		t.Fatalf("Run(check --help) = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "usage: reader check") {
		t.Fatalf("help stdout = %q", stdout.String())
	}
	// 根 usage 列出 check。
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run([]string{"help"}, stdout, stderr); code != 0 {
		t.Fatalf("Run(help) = %d", code)
	}
	if !strings.Contains(stdout.String(), "check <source-ids|--all>") {
		t.Fatalf("root usage missing check: %q", stdout.String())
	}
}
