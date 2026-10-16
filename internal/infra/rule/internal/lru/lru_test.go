package lru

import (
	"strconv"
	"sync"
	"testing"
)

func TestCacheReusesAndEvictsLeastRecentlyUsed(t *testing.T) {
	cache := New[string, int](2)
	calls := 0
	compute := func(v int) func() int {
		return func() int {
			calls++
			return v
		}
	}
	if got := cache.GetOrCompute("a", compute(1)); got != 1 {
		t.Fatalf("a = %d", got)
	}
	if got := cache.GetOrCompute("a", compute(99)); got != 1 || calls != 1 {
		t.Fatalf("cached a = %d calls = %d", got, calls)
	}
	cache.GetOrCompute("b", compute(2))
	cache.GetOrCompute("a", compute(99)) // a 变为最近使用
	cache.GetOrCompute("c", compute(3))  // 淘汰 b
	if cache.Len() != 2 {
		t.Fatalf("len = %d", cache.Len())
	}
	if got := cache.GetOrCompute("a", compute(99)); got != 1 {
		t.Fatalf("a evicted unexpectedly: %d", got)
	}
	if got := cache.GetOrCompute("b", compute(20)); got != 20 {
		t.Fatalf("b should be recomputed after eviction, got %d", got)
	}
}

func TestCacheConcurrentAccess(t *testing.T) {
	cache := New[string, string](8)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := strconv.Itoa(i % 12)
			if got := cache.GetOrCompute(key, func() string { return key }); got != key {
				t.Errorf("key %s = %s", key, got)
			}
		}(i)
	}
	wg.Wait()
	if cache.Len() > 8 {
		t.Fatalf("len = %d exceeds capacity", cache.Len())
	}
}
