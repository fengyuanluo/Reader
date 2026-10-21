package sourceapp

import (
	"context"
	"errors"
	"fmt"

	"reader/internal/domain/source"
)

type Manager struct {
	repo source.BookSourceRepository
	// limiter 在书源删除/改名时清理单源限流信号量，避免旧 bookSourceUrl 键残留（S09-02/S01-07）。
	limiter SourceLimiter
}

// SourceLimiter 是 Manager 需要的限流器最小接口，避免 app 层直接依赖实现细节。
// key 语义与搜索/发现限流一致：bookSourceUrl。
type SourceLimiter interface {
	RemoveSource(key string)
}

func NewManager(repo source.BookSourceRepository) *Manager {
	return &Manager{repo: repo}
}

// WithLimiter 注入限流器钩子（bootstrap 装配时调用）。
func (m *Manager) WithLimiter(limiter SourceLimiter) *Manager {
	m.limiter = limiter
	return m
}

func (m *Manager) List(ctx context.Context, filter source.BookSourceFilter) (source.Page[*source.BookSource], error) {
	return m.repo.List(ctx, filter)
}

func (m *Manager) Create(ctx context.Context, src *source.BookSource) (*source.BookSource, error) {
	if err := src.Normalize(); err != nil {
		return nil, err
	}
	if err := m.repo.Save(ctx, src); err != nil {
		return nil, err
	}
	return m.repo.FindByID(ctx, src.SourceID)
}

func (m *Manager) Get(ctx context.Context, id string) (*source.BookSource, error) {
	return m.repo.FindByID(ctx, id)
}

func (m *Manager) Update(ctx context.Context, id string, src *source.BookSource) (*source.BookSource, error) {
	old, err := m.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// 先用路径 id 补齐 sourceId 再 Normalize，否则改 bookSourceUrl 时会按新地址派生出不同 id。
	if src.SourceID != "" && src.SourceID != id {
		return nil, fmt.Errorf("%w: sourceId does not match path", source.ErrConflict)
	}
	src.SourceID = id
	if err := src.Normalize(); err != nil {
		return nil, err
	}
	if err := m.repo.Save(ctx, src); err != nil {
		return nil, err
	}
	// 改 bookSourceUrl 会使限流键变化：移除旧键（新键由 Normalize 后的地址派生）。
	if m.limiter != nil && old.BookSourceURL != src.BookSourceURL {
		m.limiter.RemoveSource(old.BookSourceURL)
	}
	return m.repo.FindByID(ctx, id)
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	// 先取删除前的地址作为限流键，与搜索/发现的 ratelimit.Do key 语义一致（bookSourceUrl）。
	old, err := m.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if err := m.repo.Delete(ctx, id); err != nil {
		return err
	}
	if m.limiter != nil {
		m.limiter.RemoveSource(old.BookSourceURL)
	}
	return nil
}

func (m *Manager) UpdateEnabled(ctx context.Context, id string, enabled bool) (*source.BookSource, error) {
	if err := m.repo.UpdateEnabled(ctx, id, enabled); err != nil {
		return nil, err
	}
	return m.repo.FindByID(ctx, id)
}

func (m *Manager) UpdateExploreEnabled(ctx context.Context, id string, enabled bool) (*source.BookSource, error) {
	if err := m.repo.UpdateExploreEnabled(ctx, id, enabled); err != nil {
		return nil, err
	}
	return m.repo.FindByID(ctx, id)
}

func (m *Manager) Reorder(ctx context.Context, orders []source.SourceOrder) (source.Page[*source.BookSource], error) {
	if len(orders) == 0 {
		return source.Page[*source.BookSource]{}, errors.New("orders is required")
	}
	if err := m.repo.Reorder(ctx, orders); err != nil {
		return source.Page[*source.BookSource]{}, err
	}
	return m.repo.List(ctx, source.BookSourceFilter{Page: 1, PageSize: len(orders)})
}
