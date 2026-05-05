package sourceapp

import (
	"context"
	"errors"

	"reader/internal/domain/source"
)

type Manager struct {
	repo source.BookSourceRepository
}

func NewManager(repo source.BookSourceRepository) *Manager {
	return &Manager{repo: repo}
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
	if _, err := m.repo.FindByID(ctx, id); err != nil {
		return nil, err
	}
	if err := src.Normalize(); err != nil {
		return nil, err
	}
	if src.SourceID != "" && src.SourceID != id {
		return nil, errors.New("sourceId does not match path")
	}
	src.SourceID = id
	if err := m.repo.Save(ctx, src); err != nil {
		return nil, err
	}
	return m.repo.FindByID(ctx, id)
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	return m.repo.Delete(ctx, id)
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
