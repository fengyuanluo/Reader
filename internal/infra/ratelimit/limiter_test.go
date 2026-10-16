package ratelimit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeClock 让限流等待不依赖真实时间。
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

func (c *fakeClock) slept() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

func newTestLimiter(opts Options) (*Limiter, *fakeClock) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	limiter := New(opts)
	limiter.now = clock.Now
	limiter.sleep = clock.Sleep
	return limiter, clock
}

func TestAcquireOnNilLimiterIsNoop(t *testing.T) {
	var limiter *Limiter
	release, err := limiter.Acquire(context.Background(), "src", "1/1000")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	release()
	if limiter.GlobalLimit() != 0 || limiter.SourceLimit() != 0 {
		t.Fatal("nil limiter should report no limits")
	}
}

func TestAcquireWithoutRateDoesNotWait(t *testing.T) {
	for _, rate := range []string{"", "0", "  ", "abc", "1/0", "0/1000", "x/y"} {
		limiter, clock := newTestLimiter(Options{})
		release, err := limiter.Acquire(context.Background(), "src", rate)
		if err != nil {
			t.Fatalf("Acquire(%q) error = %v", rate, err)
		}
		release()
		release2, err := limiter.Acquire(context.Background(), "src", rate)
		if err != nil {
			t.Fatalf("Acquire(%q) second error = %v", rate, err)
		}
		release2()
		if slept := clock.slept(); len(slept) != 0 {
			t.Fatalf("rate %q slept %v, want none", rate, slept)
		}
	}
}

func TestAcquireIntervalRateWaitsBetweenRequests(t *testing.T) {
	limiter, clock := newTestLimiter(Options{})

	first, err := limiter.Acquire(context.Background(), "src", "100")
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}
	if slept := clock.slept(); len(slept) != 0 {
		t.Fatalf("first Acquire slept %v, want none", slept)
	}
	first()

	second, err := limiter.Acquire(context.Background(), "src", "100")
	if err != nil {
		t.Fatalf("second Acquire() error = %v", err)
	}
	if slept := clock.slept(); len(slept) != 1 || slept[0] != 100*time.Millisecond {
		t.Fatalf("second Acquire slept %v, want one 100ms wait", slept)
	}
	second()
}

func TestAcquireConcurrentRateResetsWindow(t *testing.T) {
	limiter, clock := newTestLimiter(Options{})

	for i := range 3 {
		release, err := limiter.Acquire(context.Background(), "src", "2/1000")
		if err != nil {
			t.Fatalf("Acquire(%d) error = %v", i, err)
		}
		release()
	}
	if slept := clock.slept(); len(slept) != 0 {
		t.Fatalf("slept %v, want none within window", slept)
	}

	release, err := limiter.Acquire(context.Background(), "src", "2/1000")
	if err != nil {
		t.Fatalf("over-window Acquire() error = %v", err)
	}
	release()
	if slept := clock.slept(); len(slept) != 1 || slept[0] != time.Second {
		t.Fatalf("slept %v, want one 1s wait", slept)
	}

	// 窗口重置后重新放行，无需等待。
	release, err = limiter.Acquire(context.Background(), "src", "2/1000")
	if err != nil {
		t.Fatalf("after reset Acquire() error = %v", err)
	}
	release()
	if slept := clock.slept(); len(slept) != 1 {
		t.Fatalf("slept %v, want still one wait", slept)
	}
}

func TestAcquireRateIsTrackedPerSource(t *testing.T) {
	limiter, clock := newTestLimiter(Options{})

	releaseA, err := limiter.Acquire(context.Background(), "a", "100")
	if err != nil {
		t.Fatalf("Acquire(a) error = %v", err)
	}
	releaseB, err := limiter.Acquire(context.Background(), "b", "100")
	if err != nil {
		t.Fatalf("Acquire(b) error = %v", err)
	}
	if slept := clock.slept(); len(slept) != 0 {
		t.Fatalf("cross-source acquire slept %v, want none", slept)
	}
	releaseA()
	releaseB()
}

func TestAcquireGlobalLimitBlocksConcurrentRequests(t *testing.T) {
	limiter := New(Options{Global: 1})

	release, err := limiter.Acquire(context.Background(), "a", "")
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}
	done := make(chan struct{})
	go func() {
		second, err := limiter.Acquire(context.Background(), "b", "")
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

	release, err := limiter.Acquire(context.Background(), "a", "")
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}

	// 同一源阻塞。
	done := make(chan struct{})
	go func() {
		blocked, err := limiter.Acquire(context.Background(), "a", "")
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
	other, err := limiter.Acquire(context.Background(), "b", "")
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
	release, err := limiter.Acquire(context.Background(), "a", "")
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := limiter.Acquire(ctx, "b", ""); !errors.Is(err, context.Canceled) {
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
		_, _ = Do(context.Background(), limiter, "a", "", func(context.Context) (int, error) {
			panic("boom")
		})
	}()

	done := make(chan struct{})
	go func() {
		if _, err := Do(context.Background(), limiter, "a", "", func(context.Context) (int, error) {
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
	got, err := Do(context.Background(), nil, "a", "1/1000", func(context.Context) (string, error) {
		return "ok", nil
	})
	if err != nil || got != "ok" {
		t.Fatalf("Do() = %q, %v", got, err)
	}
}

func TestDoReturnsErrorWithoutRunning(t *testing.T) {
	limiter := New(Options{Global: 1})
	release, err := limiter.Acquire(context.Background(), "a", "")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	if _, err := Do(ctx, limiter, "b", "", func(context.Context) (int, error) {
		ran = true
		return 0, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Do() error = %v, want context.Canceled", err)
	}
	if ran {
		t.Fatal("fn must not run when the permit is not acquired")
	}
}

func TestAcquireReleasesRateRecordOnRelease(t *testing.T) {
	limiter, clock := newTestLimiter(Options{})
	release, err := limiter.Acquire(context.Background(), "src", "50")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	release()
	// 记录已归还，第二次访问只需等待间隔而不是无限等待。
	second, err := limiter.Acquire(context.Background(), "src", "50")
	if err != nil {
		t.Fatalf("second Acquire() error = %v", err)
	}
	second()
	if slept := clock.slept(); len(slept) != 1 {
		t.Fatalf("slept %v, want one wait", slept)
	}
}
