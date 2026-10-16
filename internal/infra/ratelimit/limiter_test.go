package ratelimit

import (
	"context"
	"errors"
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
