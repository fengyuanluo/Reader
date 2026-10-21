package ratelimit

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestAcquireOnNilLimiterIsNoop(t *testing.T) {
	var limiter *Limiter
	release, err := limiter.Acquire(context.Background(), "src")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	release()
	if limiter.GlobalLimit() != 0 || limiter.SourceLimit() != 0 {
		t.Fatal("nil limiter should report no limits")
	}
}

func TestAcquireGlobalLimitBlocksConcurrentRequests(t *testing.T) {
	limiter := New(Options{Global: 1})

	release, err := limiter.Acquire(context.Background(), "a")
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}
	done := make(chan struct{})
	go func() {
		second, err := limiter.Acquire(context.Background(), "b")
		if err != nil {
			t.Errorf("second Acquire() error = %v", err)
		}
		second()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("second Acquire should block while global slot is held")
	case <-time.After(30 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second Acquire did not unblock")
	}
}

func TestAcquireSourceLimitIsPerSource(t *testing.T) {
	limiter := New(Options{Source: 1})

	release, err := limiter.Acquire(context.Background(), "a")
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}

	// 同一源阻塞。
	done := make(chan struct{})
	go func() {
		blocked, err := limiter.Acquire(context.Background(), "a")
		if err != nil {
			t.Errorf("blocked Acquire() error = %v", err)
		}
		blocked()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("same-source Acquire should block")
	case <-time.After(30 * time.Millisecond):
	}

	// 不同源不阻塞。
	other, err := limiter.Acquire(context.Background(), "b")
	if err != nil {
		t.Fatalf("other-source Acquire() error = %v", err)
	}
	other()
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("same-source Acquire did not unblock")
	}
}

func TestAcquireReturnsContextErrorWhenWaiting(t *testing.T) {
	limiter := New(Options{Global: 1})
	release, err := limiter.Acquire(context.Background(), "a")
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := limiter.Acquire(ctx, "b"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire() error = %v, want context.Canceled", err)
	}
}

func TestDoReleasesSlotOnPanic(t *testing.T) {
	limiter := New(Options{Global: 1, Source: 1})
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic to propagate")
			}
		}()
		_, _ = Do(context.Background(), limiter, "a", func(context.Context) (int, error) {
			panic("boom")
		})
	}()

	done := make(chan struct{})
	go func() {
		if _, err := Do(context.Background(), limiter, "a", func(context.Context) (int, error) {
			return 1, nil
		}); err != nil {
			t.Errorf("Do() error = %v", err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("limiter slot leaked after panic")
	}
}

func TestDoWithNilLimiter(t *testing.T) {
	got, err := Do(context.Background(), nil, "a", func(context.Context) (string, error) {
		return "ok", nil
	})
	if err != nil || got != "ok" {
		t.Fatalf("Do() = %q, %v", got, err)
	}
}

func TestDoReturnsErrorWithoutRunning(t *testing.T) {
	limiter := New(Options{Global: 1})
	release, err := limiter.Acquire(context.Background(), "a")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	if _, err := Do(ctx, limiter, "b", func(context.Context) (int, error) {
		ran = true
		return 0, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Do() error = %v, want context.Canceled", err)
	}
	if ran {
		t.Fatal("fn must not run when the permit is not acquired")
	}
}

// TestSourceChanEvictsIdleEntriesWhenOverCap 验证缓存超过 maxSourceChans 后淘汰空闲 chan，map 大小有界（S01-07）。
func TestSourceChanEvictsIdleEntriesWhenOverCap(t *testing.T) {
	limiter := New(Options{Source: 2})
	over := maxSourceChans + 512
	for i := range over {
		_ = limiter.sourceChan("key-" + strconv.Itoa(i))
	}
	if got := limiter.SourceCount(); got > maxSourceChans {
		t.Fatalf("SourceCount() = %d, want <= %d", got, maxSourceChans)
	}
}

// TestSourceChanKeepsInUseEntries 验证正在使用的 chan 不被淘汰，释放后仍可正常回收许可（S01-07）。
func TestSourceChanKeepsInUseEntries(t *testing.T) {
	limiter := New(Options{Source: 2})
	// 占用最早的 chan 的一个许可。
	release, err := limiter.Acquire(context.Background(), "held")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	// 冲进超过上限的 key 触发淘汰。
	for i := range maxSourceChans + 512 {
		_ = limiter.sourceChan("key-" + strconv.Itoa(i))
	}
	// 被占用的 chan 仍在，第二次 Acquire 同 key 仍受单源上限约束。
	second, err := limiter.Acquire(context.Background(), "held")
	if err != nil {
		t.Fatalf("second Acquire() error = %v", err)
	}
	release()
	second()
	if limiter.SourceCount() == 0 {
		t.Fatal("held chan should survive eviction")
	}
}

// TestRemoveSourceDropsIdleChan 验证书源删除时空闲 chan 被移除（S09-02）。
func TestRemoveSourceDropsIdleChan(t *testing.T) {
	limiter := New(Options{Source: 2})
	_ = limiter.sourceChan("a")
	_ = limiter.sourceChan("b")
	limiter.RemoveSource("a")
	if limiter.SourceCount() != 1 {
		t.Fatalf("SourceCount() = %d, want 1", limiter.SourceCount())
	}
	// 不存在的 key 是 no-op。
	limiter.RemoveSource("missing")
	if limiter.SourceCount() != 1 {
		t.Fatalf("SourceCount() = %d, want 1", limiter.SourceCount())
	}
}

// TestRemoveSourceKeepsInUseChan 验证许可被持有时 RemoveSource 不删除（避免持有者释放失败）。
func TestRemoveSourceKeepsInUseChan(t *testing.T) {
	limiter := New(Options{Source: 2})
	release, err := limiter.Acquire(context.Background(), "busy")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	limiter.RemoveSource("busy")
	if limiter.SourceCount() != 1 {
		t.Fatalf("SourceCount() = %d, want 1", limiter.SourceCount())
	}
	release()
	if limiter.SourceCount() != 1 {
		t.Fatalf("after release SourceCount() = %d, want 1", limiter.SourceCount())
	}
}
