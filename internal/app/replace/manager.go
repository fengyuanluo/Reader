package replaceapp

import (
	"context"

	"reader/internal/domain/replace"
)

type Manager struct {
	repo replace.ReplaceRuleRepository
	// onChanged 在规则创建/更新/删除成功后触发，用于让进程内规则快照缓存失效。
	onChanged func()
}

func NewManager(repo replace.ReplaceRuleRepository) *Manager {
	return &Manager{repo: repo}
}

// WithChangedCallback 注册规则变更回调（见 InvalidateRuleCache）。
func (m *Manager) WithChangedCallback(fn func()) *Manager {
	m.onChanged = fn
	return m
}

func (m *Manager) notifyChanged() {
	if m.onChanged != nil {
		m.onChanged()
	}
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

// Create 新建替换规则：ID 由仓储层（SQLite rowid）生成并回写，避免并发创建时主键碰撞。
func (m *Manager) Create(ctx context.Context, rule *replace.ReplaceRule) (*replace.ReplaceRule, error) {
	if err := m.repo.Save(ctx, rule); err != nil {
		return nil, err
	}
	m.notifyChanged()
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
	m.notifyChanged()
	return m.repo.FindByID(ctx, id)
}

func (m *Manager) Delete(ctx context.Context, id int64) error {
	if err := m.repo.Delete(ctx, id); err != nil {
		return err
	}
	m.notifyChanged()
	return nil
}

func (m *Manager) Test(ctx context.Context, rule replace.ReplaceRule, text string) (string, error) {
	return replaceText(ctx, rule, text)
}
