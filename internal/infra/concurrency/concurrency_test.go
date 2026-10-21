package concurrency

import (
	"context"
	"errors"
	"sync"

	"testing"
	"time"
)

// TestMapReturnsOnlyErrorOnFailure 验证默认语义：任一失败返回 nil 结果与最小下标错误。
func TestMapReturnsOnlyErrorOnFailure(t *testing.T) {
	items := []int{1, 2, 3}
	results, err := Map(context.Background(), 2, items, func(ctx context.Context, index int, item int) (int, error) {
		if item == 3 {
			return 0, errors.New("boom")
		}
		return item, nil
	})
	if err == nil || results != nil {
		t.Fatalf("results = %#v, err = %v; want nil results and error", results, err)
	}
}

// TestMapWithOptionsKeepPartial 验证 KeepPartial：2 页成功 1 页失败时部分结果可见（S01-06）。
func TestMapWithOptionsKeepPartial(t *testing.T) {
	items := []int{1, 2, 3}
	results, err := MapWithOptions(context.Background(), 4, items, func(ctx context.Context, index int, item int) (int, error) {
		if item == 2 {
			return 0, errors.New("page 2 failed")
		}
		return item * 10, nil
	}, MapOptions{KeepPartial: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(results) != 3 {
		t.Fatalf("results length = %d, want 3", len(results))
	}
	if results[0] != 10 || results[2] != 30 {
		t.Fatalf("successful partial results should survive: results = %#v", results)
	}
	if results[1] != 0 {
		t.Fatalf("failed item should stay zero value: results = %#v", results)
	}
}

// TestMapPreservesOrder 验证结果按 items 原顺序排列。
func TestMapPreservesOrder(t *testing.T) {
	items := make([]int, 20)
	for i := range items {
		items[i] = i
	}
	results, err := Map(context.Background(), 8, items, func(ctx context.Context, index int, item int) (int, error) {
		// 人为打乱完成顺序。
		time.Sleep(time.Duration(20-item) * time.Millisecond)
		return item, nil
	})
	if err != nil {
		t.Fatalf("Map() error = %v", err)
	}
	for i, got := range results {
		if got != i {
			t.Fatalf("results[%d] = %d, want %d", i, got, i)
		}
	}
}

// TestMapLimitsConcurrency 验证并发度不超过 limit。
func TestMapLimitsConcurrency(t *testing.T) {
	current := make(chan int, 64)
	max := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		for c := range current {
			if c > max {
				max = c
			}
		}
	}()
	var wg sync.WaitGroup
	_, err := Map(context.Background(), 3, make([]int, 30), func(ctx context.Context, index int, item int) (int, error) {
		wg.Add(1)
		current <- len(current) + 1
		time.Sleep(10 * time.Millisecond)
		wg.Done()
		return item, nil
	})
	if err != nil {
		t.Fatalf("Map() error = %v", err)
	}
	close(current)
	<-done
	if max > 3 {
		t.Fatalf("max concurrent = %d, want <= 3", max)
	}
}
