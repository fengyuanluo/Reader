package repositories

import (
	"context"
	"database/sql"
	"errors"

	"reader/internal/domain/replace"
)

type ReplaceRuleRepository struct {
	db *sql.DB
}

func NewReplaceRuleRepository(db *sql.DB) *ReplaceRuleRepository {
	return &ReplaceRuleRepository{db: db}
}

func (r *ReplaceRuleRepository) Save(ctx context.Context, rule *replace.ReplaceRule) error {
	if err := rule.Validate(); err != nil {
		return err
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
	rows, err := r.db.QueryContext(ctx, selectReplaceRuleSQL+` WHERE is_enabled = 1 AND `+column+` = 1
AND (scope LIKE '%' || ? || '%' OR scope LIKE '%' || ? || '%' OR scope IS NULL OR scope = '')
AND (exclude_scope IS NULL OR (exclude_scope NOT LIKE '%' || ? || '%' AND exclude_scope NOT LIKE '%' || ? || '%'))
ORDER BY sort_order ASC`, bookName, origin, bookName, origin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReplaceRuleRows(rows)
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
