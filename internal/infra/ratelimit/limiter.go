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
}

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
	}
	return ch
}
