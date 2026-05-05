package replaceapp

import (
	"context"

	"reader/internal/domain/replace"
)

type Manager struct {
	repo replace.ReplaceRuleRepository
}

func NewManager(repo replace.ReplaceRuleRepository) *Manager {
	return &Manager{repo: repo}
}

func (m *Manager) List(ctx context.Context, filter replace.ReplaceRuleFilter) ([]*replace.ReplaceRule, int, error) {
	return m.repo.List(ctx, filter)
}

func (m *Manager) Create(ctx context.Context, rule *replace.ReplaceRule) (*replace.ReplaceRule, error) {
	if rule.ID == 0 {
		rule.ID = nextRuleID()
	}
	if err := m.repo.Save(ctx, rule); err != nil {
		return nil, err
	}
	return m.repo.FindByID(ctx, rule.ID)
}

func (m *Manager) Update(ctx context.Context, id int64, rule *replace.ReplaceRule) (*replace.ReplaceRule, error) {
	rule.ID = id
	if err := m.repo.Save(ctx, rule); err != nil {
		return nil, err
	}
	return m.repo.FindByID(ctx, id)
}

func (m *Manager) Delete(ctx context.Context, id int64) error {
	return m.repo.Delete(ctx, id)
}

func (m *Manager) Test(ctx context.Context, rule replace.ReplaceRule, text string) (string, error) {
	return replaceText(ctx, rule, text)
}
