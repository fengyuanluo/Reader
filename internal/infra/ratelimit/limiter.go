// Package ratelimit 按源限流并限制全局/单源并发，供搜索、发现等抓取入口复用。
//
// 速率语义对齐 Legado ConcurrentRateLimiter（help/ConcurrentRateLimiter.kt）：
//   - concurrentRate 为空或 "0"：不限流；
//   - "n"：同一源两次访问之间至少间隔 n 毫秒（同一时刻只允许一个请求）；
//   - "n/ms"：每个 ms 毫秒的窗口内最多允许 n 次访问，超出则等待到窗口结束；
//     首次建记录即计入一次访问，因此与 Legado 相同，首个窗口最多放行 n+1 次。
//
// 额外提供全局并发与单源并发信号量，分别对应 READER_GLOBAL_CONCURRENCY 与
// READER_SOURCE_CONCURRENCY，抓取入口在发起请求前调用 Acquire。
package ratelimit

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options 描述并发上限；小于等于 0 表示不限制。
type Options struct {
	Global int
	Source int
}

// Limiter 组合全局并发、单源并发与按源速率限制，可安全并发使用。
type Limiter struct {
	global chan struct{}

	mu          sync.Mutex
	sourceLimit int
	sources     map[string]chan struct{}
	records     map[string]*record

	// now / sleep 便于测试注入，未设置时使用真实时钟。
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

type record struct {
	// isConcurrent 表示 concurrentRate 采用 "次数/毫秒" 形式。
	isConcurrent bool
	// time 为窗口起始（毫秒时间戳），frequency 为本窗口已放行次数或进行中的请求数。
	time      int64
	frequency int
}

// New 创建限流器。
func New(opts Options) *Limiter {
	limiter := &Limiter{
		sourceLimit: opts.Source,
		sources:     map[string]chan struct{}{},
		records:     map[string]*record{},
		now:         time.Now,
		sleep:       sleepContext,
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

// Do 在限流许可下执行 fn，无论 fn 正常返回还是 panic 都会释放许可，避免许可泄漏。
// limiter 为 nil 时不限流。
func Do[T any](ctx context.Context, limiter *Limiter, key string, concurrentRate string, fn func(context.Context) (T, error)) (T, error) {
	release, err := limiter.Acquire(ctx, key, concurrentRate)
	if err != nil {
		var zero T
		return zero, err
	}
	defer release()
	return fn(ctx)
}

// Acquire 获取一次访问许可并返回释放函数，释放函数必须且只能调用一次。
// key 为书源标识（bookSourceUrl），concurrentRate 为书源的并发率配置。
// ctx 取消时返回 ctx 的错误。
func (l *Limiter) Acquire(ctx context.Context, key string, concurrentRate string) (func(), error) {
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
	rec, err := l.waitRate(ctx, key, concurrentRate)
	if err != nil {
		release()
		return nil, err
	}
	if rec != nil {
		inner := release
		release = func() {
			l.fetchEnd(rec)
			inner()
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

// waitRate 按 concurrentRate 等待到允许访问，返回需要记账的并发记录（不限流时为 nil）。
func (l *Limiter) waitRate(ctx context.Context, key string, concurrentRate string) (*record, error) {
	rate := strings.TrimSpace(concurrentRate)
	if key == "" || rate == "" || rate == "0" {
		return nil, nil
	}
	index := strings.Index(rate, "/")
	for {
		rec, waitMillis := l.rateStep(key, rate, index)
		if rec == nil {
			// 配置无法解析时按 Legado 的异常分支处理：视为不限制。
			return nil, nil
		}
		if waitMillis <= 0 {
			return rec, nil
		}
		if err := l.wait(ctx, time.Duration(waitMillis)*time.Millisecond); err != nil {
			return nil, err
		}
	}
}

// rateStep 推进一次限流判定，返回并发记录与需要等待的毫秒数（0 表示可立即访问）。
// 对齐 Legado ConcurrentRateLimiter.fetchStart：首次访问直接放行并建立记录。
func (l *Limiter) rateStep(key string, rate string, index int) (*record, int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now().UnixMilli()
	rec := l.records[key]
	if rec == nil {
		isConcurrent := index > 0
		if isConcurrent {
			if _, _, ok := parseRate(rate[:index], rate[index+1:]); !ok {
				return nil, 0
			}
		} else if _, err := strconv.Atoi(rate); err != nil {
			return nil, 0
		}
		rec = &record{isConcurrent: isConcurrent, time: now, frequency: 1}
		l.records[key] = rec
		return rec, 0
	}
	if rec.isConcurrent {
		count, window, ok := parseRate(rate[:index], rate[index+1:])
		if !ok {
			return nil, 0
		}
		next := rec.time + window
		if now >= next {
			rec.time = now
			rec.frequency = 1
			return rec, 0
		}
		if int64(rec.frequency) > count {
			return rec, next - now
		}
		rec.frequency++
		return rec, 0
	}
	gap, err := strconv.Atoi(rate)
	if err != nil {
		return nil, 0
	}
	if rec.frequency > 0 {
		return rec, int64(gap)
	}
	next := rec.time + int64(gap)
	if now >= next {
		rec.time = now
		rec.frequency = 1
		return rec, 0
	}
	return rec, next - now
}

// fetchEnd 对齐 Legado ConcurrentRateLimiter.fetchEnd：只有间隔模式需要回收计数。
func (l *Limiter) fetchEnd(rec *record) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !rec.isConcurrent && rec.frequency > 0 {
		rec.frequency--
	}
}

// parseRate 解析 "次数/毫秒"；非法或非正数视为不可用。
func parseRate(countText string, windowText string) (int64, int64, bool) {
	count, err := strconv.ParseInt(strings.TrimSpace(countText), 10, 64)
	if err != nil || count <= 0 {
		return 0, 0, false
	}
	window, err := strconv.ParseInt(strings.TrimSpace(windowText), 10, 64)
	if err != nil || window <= 0 {
		return 0, 0, false
	}
	return count, window, true
}

func (l *Limiter) wait(ctx context.Context, d time.Duration) error {
	if l.sleep != nil {
		return l.sleep(ctx, d)
	}
	return sleepContext(ctx, d)
}

// sleepContext 在等待期间响应 context 取消。
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
