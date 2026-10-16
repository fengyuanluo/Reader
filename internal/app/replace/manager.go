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

// replaceRuleListAllPageSize 是“拉全量替换规则”的取数上限；该策略属于应用层，不下沉到 handler。
const replaceRuleListAllPageSize = 10000

// ListAll 返回全部替换规则，供 GET /replace-rules 这类无分页入参的入口使用。
func (m *Manager) ListAll(ctx context.Context) ([]*replace.ReplaceRule, error) {
	items, _, err := m.List(ctx, replace.ReplaceRuleFilter{Page: 1, PageSize: replaceRuleListAllPageSize})
	if err != nil {
		return nil, err
	}
	return items, nil
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
