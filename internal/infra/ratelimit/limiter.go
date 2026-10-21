// Package ratelimit 限制全局与单源抓取并发，供搜索、发现等抓取入口复用。
//
// 按源 concurrentRate 的速率限流统一由 fetcher 承担（internal/infra/fetcher/rate_limiter.go，
// 由 fetch.Request 的 SourceKey / ConcurrentRate / SkipRateLimit 驱动），因此这里只保留
// 全局并发与单源并发信号量，分别对应 READER_GLOBAL_CONCURRENCY 与 READER_SOURCE_CONCURRENCY，
// 避免同一请求被两处限速。
package ratelimit

import (
	"context"
	"sync"
)

// Options 描述并发上限；小于等于 0 表示不限制。
type Options struct {
	Global int
	Source int
}

// Limiter 组合全局并发与单源并发信号量，可安全并发使用。
type Limiter struct {
	global chan struct{}

	mu          sync.Mutex
	sourceLimit int
	sources     map[string]chan struct{}
	// sourceOrder 记录 key 的首次出现顺序，用于在超过 maxSourceChans 时淘汰最早空闲的 chan（S01-07）。
	sourceOrder []string
}

// maxSourceChans 限制单源信号量缓存条目数：长期运行进程随源数线性增长，超限后淘汰最早出现且
// 当前空闲的 chan；正在使用的 chan 不淘汰（容量即使用计数，缓冲满时必有等待者）。
const maxSourceChans = 4096

// New 创建限流器。
func New(opts Options) *Limiter {
	limiter := &Limiter{
		sourceLimit: opts.Source,
		sources:     map[string]chan struct{}{},
	}
	if opts.Global > 0 {
		limiter.global = make(chan struct{}, opts.Global)
	}
	return limiter
}

// GlobalLimit 返回全局并发上限；小于等于 0 表示不限制，调用方可据此决定抓取任务的并发度。
func (l *Limiter) GlobalLimit() int {
	if l == nil {
		return 0
	}
	return cap(l.global)
}

// SourceLimit 返回单源并发上限；小于等于 0 表示不限制。
func (l *Limiter) SourceLimit() int {
	if l == nil {
		return 0
	}
	return l.sourceLimit
}

// Do 在并发许可下执行 fn，无论 fn 正常返回还是 panic 都会释放许可，避免许可泄漏。
// limiter 为 nil 时不限并发。
func Do[T any](ctx context.Context, limiter *Limiter, key string, fn func(context.Context) (T, error)) (T, error) {
	release, err := limiter.Acquire(ctx, key)
	if err != nil {
		var zero T
		return zero, err
	}
	defer release()
	return fn(ctx)
}

// Acquire 获取一次并发许可并返回释放函数，释放函数必须且只能调用一次。
// key 为书源标识（bookSourceUrl），仅参与单源并发上限。ctx 取消时返回 ctx 的错误。
func (l *Limiter) Acquire(ctx context.Context, key string) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	release := func() {}
	if l.global != nil {
		select {
		case l.global <- struct{}{}:
			global := l.global
			release = func() { <-global }
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if source := l.sourceChan(key); source != nil {
		select {
		case source <- struct{}{}:
			inner := release
			release = func() {
				<-source
				inner()
			}
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		}
	}
	return release, nil
}

// sourceChan 返回 key 对应的单源并发信号量；未配置单源上限时返回 nil。
// 缓存超过 maxSourceChans 时淘汰最早出现且空闲的 chan，保证 map 大小有界（S01-07/S09-02）。
func (l *Limiter) sourceChan(key string) chan struct{} {
	if l.sourceLimit <= 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ch := l.sources[key]
	if ch == nil {
		ch = make(chan struct{}, l.sourceLimit)
		l.sources[key] = ch
		l.sourceOrder = append(l.sourceOrder, key)
		l.evictLocked()
	}
	return ch
}

// evictLocked 在超过 maxSourceChans 时淘汰最早出现且当前空闲（缓冲未满）的 chan。
// 持有 l.mu 时调用。
func (l *Limiter) evictLocked() {
	if len(l.sourceOrder) <= maxSourceChans {
		return
	}
	kept := l.sourceOrder[:0]
	for _, key := range l.sourceOrder {
		ch, ok := l.sources[key]
		if ok && len(l.sourceOrder)-len(kept) > len(l.sourceOrder)-maxSourceChans && len(ch) < cap(ch) {
			// 空闲 chan（无持有许可）可安全删除；正在使用的 chan 继续保留。
			delete(l.sources, key)
			continue
		}
		kept = append(kept, key)
	}
	l.sourceOrder = kept
}

// RemoveSource 在书源删除/改名时移除对应的单源信号量，避免旧键残留（S09-02）。
// key 为 bookSourceUrl。
func (l *Limiter) RemoveSource(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ch, ok := l.sources[key]
	if !ok {
		return
	}
	// 正在使用的 chan（许可被持有）不能删除，否则持有者释放时回收失败。
	if len(ch) > 0 {
		return
	}
	delete(l.sources, key)
	for i, existing := range l.sourceOrder {
		if existing == key {
			l.sourceOrder = append(l.sourceOrder[:i], l.sourceOrder[i+1:]...)
			break
		}
	}
}

// SourceCount 返回当前缓存的单源信号量数量，供测试与观测断言 map 有界。
func (l *Limiter) SourceCount() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.sources)
}
