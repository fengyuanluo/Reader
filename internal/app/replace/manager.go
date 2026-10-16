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

// Create 新建替换规则：ID 由仓储层（SQLite rowid）生成并回写，避免并发创建时主键碰撞。
func (m *Manager) Create(ctx context.Context, rule *replace.ReplaceRule) (*replace.ReplaceRule, error) {
	if err := m.repo.Save(ctx, rule); err != nil {
		return nil, err
	}
	return m.repo.FindByID(ctx, rule.ID)
}

// Update 按 ID 覆盖替换规则：ID 不存在时返回仓储层的 NotFound（HTTP 映射 404），
// 不静默 upsert 出多余记录。
func (m *Manager) Update(ctx context.Context, id int64, rule *replace.ReplaceRule) (*replace.ReplaceRule, error) {
	if _, err := m.repo.FindByID(ctx, id); err != nil {
		return nil, err
	}
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
