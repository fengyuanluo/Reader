package cli

import (
	"bytes"
	"io"
	"testing"

	"reader/internal/bootstrap"
)

// TestSearchConcurrencyOverridesGlobalConcurrency 断言 --concurrency 32 会把
// globalConcurrency 传给 open，使其在 config.Load 后覆盖 READER_GLOBAL_CONCURRENCY。
func TestSearchConcurrencyOverridesGlobalConcurrency(t *testing.T) {
	var gotConcurrency int
	env := &cmdEnv{
		ctx: t.Context(),
		open: func(stderr io.Writer, globalConcurrency int) (*bootstrap.Runtime, func(), int) {
			gotConcurrency = globalConcurrency
			return newTestRuntime(t), func() {}, 0
		},
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runSearch(env, []string{"斗破", "--concurrency", "32", "--source", "anything"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("runSearch(--concurrency 32) = %d, stderr: %s", code, stderr.String())
	}
	if gotConcurrency != 32 {
		t.Fatalf("open 收到的 globalConcurrency = %d, want 32", gotConcurrency)
	}
}

// TestSearchNegativeTimeoutUsageError 断言 --timeout 为负报用法错退出码 1，且不打开运行时。
func TestSearchNegativeTimeoutUsageError(t *testing.T) {
	env := &cmdEnv{
		ctx: t.Context(),
		open: func(stderr io.Writer, globalConcurrency int) (*bootstrap.Runtime, func(), int) {
			t.Errorf("runtime must not be opened for usage errors")
			return nil, nil, 2
		},
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runSearch(env, []string{"斗破", "--timeout", "-5s"}, stdout, stderr)
	if code != 1 {
		t.Fatalf("runSearch(--timeout -5s) = %d, want 1, stderr: %s", code, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("--timeout")) {
		t.Fatalf("stderr 应说明 --timeout: %q", stderr.String())
	}
}
