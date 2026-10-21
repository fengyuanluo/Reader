package repositories

import (
	"context"
	"database/sql"
	"errors"

	"reader/internal/domain/replace"
	"reader/internal/domain/source"
)

type ReplaceRuleRepository struct {
	db *sql.DB
}

func NewReplaceRuleRepository(db *sql.DB) *ReplaceRuleRepository {
	return &ReplaceRuleRepository{db: db}
}

// Save 写入替换规则：rule.ID 为 0 时由 SQLite 自增 rowid 生成主键并回写到 rule，
// 避免上层用时间戳生成主键在并发创建时碰撞；ID 非 0 时按主键 upsert。
func (r *ReplaceRuleRepository) Save(ctx context.Context, rule *replace.ReplaceRule) error {
	if err := rule.Validate(); err != nil {
		return err
	}
	if rule.ID == 0 {
		result, err := r.db.ExecContext(ctx, `
INSERT INTO replace_rules(name, rule_group, pattern, replacement, scope, scope_title, scope_content, exclude_scope, is_enabled, is_regex, timeout_millisecond, sort_order)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			rule.Name, nullable(rule.Group), rule.Pattern, rule.Replacement, nullable(rule.Scope), boolInt(rule.ScopeTitle), boolInt(rule.ScopeContent), nullable(rule.ExcludeScope), boolInt(rule.IsEnabled), boolInt(rule.IsRegex), rule.ValidTimeoutMillisecond(), rule.Order)
		if err != nil {
			return err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		rule.ID = id
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO replace_rules(id, name, rule_group, pattern, replacement, scope, scope_title, scope_content, exclude_scope, is_enabled, is_regex, timeout_millisecond, sort_order)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  name = excluded.name,
  rule_group = excluded.rule_group,
  pattern = excluded.pattern,
  replacement = excluded.replacement,
  scope = excluded.scope,
  scope_title = excluded.scope_title,
  scope_content = excluded.scope_content,
  exclude_scope = excluded.exclude_scope,
  is_enabled = excluded.is_enabled,
  is_regex = excluded.is_regex,
  timeout_millisecond = excluded.timeout_millisecond,
  sort_order = excluded.sort_order`,
		rule.ID, rule.Name, nullable(rule.Group), rule.Pattern, rule.Replacement, nullable(rule.Scope), boolInt(rule.ScopeTitle), boolInt(rule.ScopeContent), nullable(rule.ExcludeScope), boolInt(rule.IsEnabled), boolInt(rule.IsRegex), rule.ValidTimeoutMillisecond(), rule.Order)
	return err
}

func (r *ReplaceRuleRepository) FindByID(ctx context.Context, id int64) (*replace.ReplaceRule, error) {
	return scanReplaceRule(r.db.QueryRowContext(ctx, selectReplaceRuleSQL+` WHERE id = ?`, id))
}

func (r *ReplaceRuleRepository) List(ctx context.Context, filter replace.ReplaceRuleFilter) ([]*replace.ReplaceRule, int, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize < 1 {
		pageSize = 50
	}
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM replace_rules`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, selectReplaceRuleSQL+` ORDER BY sort_order ASC LIMIT ? OFFSET ?`, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items, err := scanReplaceRuleRows(rows)
	return items, total, err
}

func (r *ReplaceRuleRepository) Delete(ctx context.Context, id int64) error {
	return execUpdateOne(ctx, r.db, `DELETE FROM replace_rules WHERE id = ?`, id)
}

func (r *ReplaceRuleRepository) FindEnabledByTitleScope(ctx context.Context, bookName, origin string) ([]*replace.ReplaceRule, error) {
	return r.findEnabledByScope(ctx, "scope_title", bookName, origin)
}

func (r *ReplaceRuleRepository) FindEnabledByContentScope(ctx context.Context, bookName, origin string) ([]*replace.ReplaceRule, error) {
	return r.findEnabledByScope(ctx, "scope_content", bookName, origin)
}

func (r *ReplaceRuleRepository) findEnabledByScope(ctx context.Context, column string, bookName, origin string) ([]*replace.ReplaceRule, error) {
	// 只在 SQL 里筛启用与 scope 开关（可用索引缩小扫描面），scope/exclude_scope 的
	// 项匹配移到应用层按分隔符精确匹配（S11-05）：SQL LIKE '%' || ? || '%' 的子串
	// 语义会误命中包含关系（书名 "斗" 命中 scope "斗破"），对齐 Legado 按项精确匹配。
	rows, err := r.db.QueryContext(ctx, selectReplaceRuleSQL+` WHERE is_enabled = 1 AND `+column+` = 1
ORDER BY sort_order ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all, err := scanReplaceRuleRows(rows)
	if err != nil {
		return nil, err
	}
	items := make([]*replace.ReplaceRule, 0, len(all))
	for _, r := range all {
		if !matchesScope(r.Scope, bookName, origin) {
			continue
		}
		if r.ExcludeScope != "" && matchesScope(r.ExcludeScope, bookName, origin) {
			continue
		}
		items = append(items, r)
	}
	return items, nil
}

// matchesScope 判断书名/源名是否命中 scope 项列表：空 scope 表示不限制（全部命中）；
// 非空时按 SplitGroups 同样的分隔符（[,;，；]）拆项，任一项与 bookName 或 origin
// 精确相等才命中，消除 SQL LIKE 子串的包含误匹配（S11-05）。
func matchesScope(scope, bookName, origin string) bool {
	if scope == "" {
		return true
	}
	for _, item := range source.SplitGroups(scope) {
		if item == bookName || item == origin {
			return true
		}
	}
	return false
}

const selectReplaceRuleSQL = `SELECT id, name, coalesce(rule_group, ''), pattern, replacement, coalesce(scope, ''),
scope_title, scope_content, coalesce(exclude_scope, ''), is_enabled, is_regex, timeout_millisecond, sort_order FROM replace_rules`

func scanReplaceRuleRows(rows *sql.Rows) ([]*replace.ReplaceRule, error) {
	items := []*replace.ReplaceRule{}
	for rows.Next() {
		item, err := scanReplaceRule(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanReplaceRule(s scanner) (*replace.ReplaceRule, error) {
	var rule replace.ReplaceRule
	var scopeTitle, scopeContent, isEnabled, isRegex int
	err := s.Scan(&rule.ID, &rule.Name, &rule.Group, &rule.Pattern, &rule.Replacement, &rule.Scope,
		&scopeTitle, &scopeContent, &rule.ExcludeScope, &isEnabled, &isRegex, &rule.TimeoutMillisecond, &rule.Order)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rule.ScopeTitle = scopeTitle == 1
	rule.ScopeContent = scopeContent == 1
	rule.IsEnabled = isEnabled == 1
	rule.IsRegex = isRegex == 1
	return &rule, nil
}
