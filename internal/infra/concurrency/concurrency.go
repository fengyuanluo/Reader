// Package concurrency 提供受控并发的通用执行工具，供正文多分页、目录多分页等场景复用，
// 避免各调用方各写一份 worker 数量控制与错误收敛逻辑。
package concurrency

import (
	"context"
	"sync"
)

// defaultLimit 是未显式指定并发度时的 worker 上限。
const defaultLimit = 8

// Map 以最多 limit 个并发对 items 调用 fn，返回按 items 原顺序排列的结果。
// limit < 1 时使用 defaultLimit。任一项失败或 ctx 取消时，Map 会取消其余任务，
// 并在等待全部 worker 退出后返回最小下标对应的错误，保证同一输入下错误稳定。
func Map[T any, R any](ctx context.Context, limit int, items []T, fn func(ctx context.Context, index int, item T) (R, error)) ([]R, error) {
	if limit < 1 {
		limit = defaultLimit
	}
	if len(items) == 0 {
		return nil, nil
	}
	if limit > len(items) {
		limit = len(items)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]R, len(items))
	errs := make([]error, len(items))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := range items {
		wg.Add(1)
		go func(index int, item T) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errs[index] = ctx.Err()
				return
			}
			defer func() { <-sem }()
			if err := ctx.Err(); err != nil {
				errs[index] = err
				return
			}
			value, err := fn(ctx, index, item)
			if err != nil {
				errs[index] = err
				cancel()
				return
			}
			results[index] = value
		}(i, items[i])
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}
