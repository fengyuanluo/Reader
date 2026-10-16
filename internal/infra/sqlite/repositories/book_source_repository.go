package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"reader/internal/domain/source"
)

var ErrNotFound = source.ErrNotFound

type BookSourceRepository struct {
	db *sql.DB
}

func NewBookSourceRepository(db *sql.DB) *BookSourceRepository {
	return &BookSourceRepository{db: db}
}

// Save 写入单个书源，语义同 SaveMany。
func (r *BookSourceRepository) Save(ctx context.Context, src *source.BookSource) error {
	return r.SaveMany(ctx, []*source.BookSource{src})
}

// SaveMany 在单个事务内写入全部书源，任一失败整批回滚。
// 以 sourceId 与 bookSourceUrl 双键 upsert：同 sourceId 换地址即改名（对齐 Legado 编辑书源时删旧插新，
// 并清理旧地址的书源变量与缓存）；同地址换 sourceId 则覆盖。若新地址已属于另一书源，返回 source.ErrConflict。
func (r *BookSourceRepository) SaveMany(ctx context.Context, sources []*source.BookSource) error {
	args := make([][]any, len(sources))
	for i, src := range sources {
		if err := src.Normalize(); err != nil {
			return err
		}
		a, err := bookSourceArgs(src)
		if err != nil {
			return err
		}
		args[i] = a
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	lookup, err := tx.PrepareContext(ctx, `SELECT book_source_url FROM book_sources WHERE source_id = ?`)
	if err != nil {
		return err
	}
	defer lookup.Close()
	upsert, err := tx.PrepareContext(ctx, upsertBookSourceSQL)
	if err != nil {
		return err
	}
	defer upsert.Close()
	for i, src := range sources {
		var oldURL string
		if err := lookup.QueryRowContext(ctx, src.SourceID).Scan(&oldURL); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := upsert.ExecContext(ctx, args[i]...); err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("%w: sourceId=%q bookSourceUrl=%q", source.ErrConflict, src.SourceID, src.BookSourceURL)
			}
			return err
		}
		if oldURL != "" && oldURL != src.BookSourceURL {
			if err := deleteSourceOwnedData(ctx, tx, oldURL); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (r *BookSourceRepository) FindByID(ctx context.Context, id string) (*source.BookSource, error) {
	return scanBookSource(r.db.QueryRowContext(ctx, selectBookSourceSQL+` WHERE source_id = ?`, id))
}

func (r *BookSourceRepository) FindByURL(ctx context.Context, sourceURL string) (*source.BookSource, error) {
	return scanBookSource(r.db.QueryRowContext(ctx, selectBookSourceSQL+` WHERE book_source_url = ?`, sourceURL))
}

func (r *BookSourceRepository) List(ctx context.Context, filter source.BookSourceFilter) (source.Page[*source.BookSource], error) {
	where, args := buildBookSourceWhere(filter)
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize < 1 {
		pageSize = 50
	}
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM book_sources`+where, args...).Scan(&total); err != nil {
		return source.Page[*source.BookSource]{}, err
	}
	queryArgs := append(append([]any{}, args...), pageSize, (page-1)*pageSize)
	rows, err := r.db.QueryContext(ctx, selectBookSourceSQL+where+` ORDER BY custom_order ASC, book_source_name ASC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return source.Page[*source.BookSource]{}, err
	}
	defer rows.Close()
	items := []*source.BookSource{}
	for rows.Next() {
		src, err := scanBookSource(rows)
		if err != nil {
			return source.Page[*source.BookSource]{}, err
		}
		items = append(items, src)
	}
	if err := rows.Err(); err != nil {
		return source.Page[*source.BookSource]{}, err
	}
	return source.Page[*source.BookSource]{Items: items, Total: total}, nil
}

func (r *BookSourceRepository) Delete(ctx context.Context, id string) error {
	src, err := r.FindByID(ctx, id)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx, `DELETE FROM book_sources WHERE source_id = ?`, id); err != nil {
		return err
	}
	if err := deleteSourceOwnedData(ctx, tx, src.BookSourceURL); err != nil {
		return err
	}
	return tx.Commit()
}

// deleteSourceOwnedData 删除以书源地址为键的书源变量与书源缓存。
func deleteSourceOwnedData(ctx context.Context, q queryer, bookSourceURL string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM source_variables WHERE book_source_url = ?`, bookSourceURL); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `DELETE FROM cache_entries WHERE owner_type = 'source' AND owner_id = ?`, bookSourceURL)
	return err
}

func (r *BookSourceRepository) UpdateEnabled(ctx context.Context, id string, enabled bool) error {
	return execUpdateOne(ctx, r.db, `UPDATE book_sources SET enabled = ?, updated_at = CURRENT_TIMESTAMP WHERE source_id = ?`, boolInt(enabled), id)
}

func (r *BookSourceRepository) UpdateExploreEnabled(ctx context.Context, id string, enabled bool) error {
	return execUpdateOne(ctx, r.db, `UPDATE book_sources SET enabled_explore = ?, updated_at = CURRENT_TIMESTAMP WHERE source_id = ?`, boolInt(enabled), id)
}

func (r *BookSourceRepository) Reorder(ctx context.Context, orders []source.SourceOrder) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	for _, order := range orders {
		if err := execUpdateOne(ctx, tx, `UPDATE book_sources SET custom_order = ?, updated_at = CURRENT_TIMESTAMP WHERE source_id = ?`, order.CustomOrder, order.SourceID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// upsertBookSourceSQL 先按 source_id 冲突更新（含改地址），再按 book_source_url 冲突更新（含改 sourceId）。
const upsertBookSourceSQL = `
INSERT INTO book_sources (
  source_id, book_source_url, book_source_name, book_source_group, book_source_type, book_url_pattern,
  custom_order, enabled, enabled_explore, js_lib, enabled_cookie_jar, concurrent_rate, header, login_url,
  login_ui, login_check_js, cover_decode_js, book_source_comment, variable_comment, last_update_time,
  respond_time, weight, explore_url, explore_screen, rule_explore, search_url, rule_search, rule_book_info,
  rule_toc, rule_content, rule_review, raw_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(source_id) DO UPDATE SET
  book_source_url = excluded.book_source_url,
  book_source_name = excluded.book_source_name,
  book_source_group = excluded.book_source_group,
  book_source_type = excluded.book_source_type,
  book_url_pattern = excluded.book_url_pattern,
  custom_order = excluded.custom_order,
  enabled = excluded.enabled,
  enabled_explore = excluded.enabled_explore,
  js_lib = excluded.js_lib,
  enabled_cookie_jar = excluded.enabled_cookie_jar,
  concurrent_rate = excluded.concurrent_rate,
  header = excluded.header,
  login_url = excluded.login_url,
  login_ui = excluded.login_ui,
  login_check_js = excluded.login_check_js,
  cover_decode_js = excluded.cover_decode_js,
  book_source_comment = excluded.book_source_comment,
  variable_comment = excluded.variable_comment,
  last_update_time = excluded.last_update_time,
  respond_time = excluded.respond_time,
  weight = excluded.weight,
  explore_url = excluded.explore_url,
  explore_screen = excluded.explore_screen,
  rule_explore = excluded.rule_explore,
  search_url = excluded.search_url,
  rule_search = excluded.rule_search,
  rule_book_info = excluded.rule_book_info,
  rule_toc = excluded.rule_toc,
  rule_content = excluded.rule_content,
  rule_review = excluded.rule_review,
  raw_json = excluded.raw_json,
  updated_at = CURRENT_TIMESTAMP
ON CONFLICT(book_source_url) DO UPDATE SET
  source_id = excluded.source_id,
  book_source_name = excluded.book_source_name,
  book_source_group = excluded.book_source_group,
  book_source_type = excluded.book_source_type,
  book_url_pattern = excluded.book_url_pattern,
  custom_order = excluded.custom_order,
  enabled = excluded.enabled,
  enabled_explore = excluded.enabled_explore,
  js_lib = excluded.js_lib,
  enabled_cookie_jar = excluded.enabled_cookie_jar,
  concurrent_rate = excluded.concurrent_rate,
  header = excluded.header,
  login_url = excluded.login_url,
  login_ui = excluded.login_ui,
  login_check_js = excluded.login_check_js,
  cover_decode_js = excluded.cover_decode_js,
  book_source_comment = excluded.book_source_comment,
  variable_comment = excluded.variable_comment,
  last_update_time = excluded.last_update_time,
  respond_time = excluded.respond_time,
  weight = excluded.weight,
  explore_url = excluded.explore_url,
  explore_screen = excluded.explore_screen,
  rule_explore = excluded.rule_explore,
  search_url = excluded.search_url,
  rule_search = excluded.rule_search,
  rule_book_info = excluded.rule_book_info,
  rule_toc = excluded.rule_toc,
  rule_content = excluded.rule_content,
  rule_review = excluded.rule_review,
  raw_json = excluded.raw_json,
  updated_at = CURRENT_TIMESTAMP`

// bookSourceArgs 生成 upsertBookSourceSQL 的参数。
func bookSourceArgs(src *source.BookSource) ([]any, error) {
	ruleExplore, err := json.Marshal(src.RuleExplore)
	if err != nil {
		return nil, fmt.Errorf("marshal ruleExplore: %w", err)
	}
	ruleSearch, err := json.Marshal(src.RuleSearch)
	if err != nil {
		return nil, fmt.Errorf("marshal ruleSearch: %w", err)
	}
	ruleBookInfo, err := json.Marshal(src.RuleBookInfo)
	if err != nil {
		return nil, fmt.Errorf("marshal ruleBookInfo: %w", err)
	}
	ruleToc, err := json.Marshal(src.RuleToc)
	if err != nil {
		return nil, fmt.Errorf("marshal ruleToc: %w", err)
	}
	ruleContent, err := json.Marshal(src.RuleContent)
	if err != nil {
		return nil, fmt.Errorf("marshal ruleContent: %w", err)
	}
	return []any{
		src.SourceID, src.BookSourceURL, src.BookSourceName, nullable(src.BookSourceGroup), src.BookSourceType, nullable(src.BookURLPattern),
		src.CustomOrder, boolInt(src.Enabled), boolInt(src.EnabledExplore), nullable(src.JSLib), boolInt(src.EnabledCookieJar), nullable(src.ConcurrentRate), nullable(src.Header), nullable(src.LoginURL),
		nullable(src.LoginUI), nullable(src.LoginCheckJS), nullable(src.CoverDecodeJS), nullable(src.BookSourceComment), nullable(src.VariableComment), src.LastUpdateTime,
		src.RespondTime, src.Weight, nullable(src.ExploreURL), nullable(src.ExploreScreen), string(ruleExplore), nullable(src.SearchURL), string(ruleSearch), string(ruleBookInfo),
		string(ruleToc), string(ruleContent), string(src.RuleReview), string(src.RawJSON),
	}, nil
}

// isUniqueViolation 判断错误是否为 SQLite UNIQUE / PRIMARY KEY 约束冲突。
func isUniqueViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	code := sqliteErr.Code()
	return code == sqlite3.SQLITE_CONSTRAINT_UNIQUE || code == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}

const selectBookSourceSQL = `SELECT source_id, book_source_url, book_source_name, coalesce(book_source_group, ''),
book_source_type, coalesce(book_url_pattern, ''), custom_order, enabled, enabled_explore, coalesce(js_lib, ''),
enabled_cookie_jar, coalesce(concurrent_rate, ''), coalesce(header, ''), coalesce(login_url, ''), coalesce(login_ui, ''),
coalesce(login_check_js, ''), coalesce(cover_decode_js, ''), coalesce(book_source_comment, ''), coalesce(variable_comment, ''),
last_update_time, respond_time, weight, coalesce(explore_url, ''), coalesce(explore_screen, ''), coalesce(nullif(rule_explore, ''), 'null'),
coalesce(search_url, ''), coalesce(nullif(rule_search, ''), 'null'), coalesce(nullif(rule_book_info, ''), 'null'), coalesce(nullif(rule_toc, ''), 'null'),
coalesce(nullif(rule_content, ''), 'null'), coalesce(rule_review, ''), coalesce(raw_json, '') FROM book_sources`

type scanner interface {
	Scan(dest ...any) error
}

func scanBookSource(s scanner) (*source.BookSource, error) {
	var src source.BookSource
	var enabled, enabledExplore, enabledCookieJar int
	var ruleExplore, ruleSearch, ruleBookInfo, ruleToc, ruleContent, ruleReview, rawJSON string
	err := s.Scan(
		&src.SourceID, &src.BookSourceURL, &src.BookSourceName, &src.BookSourceGroup,
		&src.BookSourceType, &src.BookURLPattern, &src.CustomOrder, &enabled, &enabledExplore, &src.JSLib,
		&enabledCookieJar, &src.ConcurrentRate, &src.Header, &src.LoginURL, &src.LoginUI,
		&src.LoginCheckJS, &src.CoverDecodeJS, &src.BookSourceComment, &src.VariableComment,
		&src.LastUpdateTime, &src.RespondTime, &src.Weight, &src.ExploreURL, &src.ExploreScreen, &ruleExplore,
		&src.SearchURL, &ruleSearch, &ruleBookInfo, &ruleToc, &ruleContent, &ruleReview, &rawJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	src.Enabled = enabled == 1
	src.EnabledExplore = enabledExplore == 1
	src.EnabledCookieJar = enabledCookieJar == 1
	for _, col := range []struct {
		name string
		raw  string
		dst  any
	}{
		{"rule_explore", ruleExplore, &src.RuleExplore},
		{"rule_search", ruleSearch, &src.RuleSearch},
		{"rule_book_info", ruleBookInfo, &src.RuleBookInfo},
		{"rule_toc", ruleToc, &src.RuleToc},
		{"rule_content", ruleContent, &src.RuleContent},
	} {
		if err := json.Unmarshal([]byte(col.raw), col.dst); err != nil {
			return nil, fmt.Errorf("书源 %s 的 %s 列 JSON 损坏: %w", src.SourceID, col.name, err)
		}
	}
	src.RuleReview = []byte(ruleReview)
	src.RawJSON = []byte(rawJSON)
	return &src, nil
}

func buildBookSourceWhere(filter source.BookSourceFilter) (string, []any) {
	clauses := []string{}
	args := []any{}
	if filter.Keyword != "" {
		clauses = append(clauses, `(book_source_name LIKE ? ESCAPE '\' OR book_source_group LIKE ? ESCAPE '\' OR book_source_url LIKE ? ESCAPE '\' OR book_source_comment LIKE ? ESCAPE '\')`)
		like := "%" + escapeLike(filter.Keyword) + "%"
		args = append(args, like, like, like, like)
	}
	if filter.Group != "" {
		clauses = append(clauses, `(book_source_group = ? OR book_source_group LIKE ? ESCAPE '\' OR book_source_group LIKE ? ESCAPE '\' OR book_source_group LIKE ? ESCAPE '\')`)
		group := escapeLike(filter.Group)
		args = append(args, filter.Group, group+",%", "%,"+group, "%,"+group+",%")
	}
	if filter.Enabled != nil {
		clauses = append(clauses, `enabled = ?`)
		args = append(args, boolInt(*filter.Enabled))
	}
	if filter.EnabledExplore != nil {
		clauses = append(clauses, `enabled_explore = ?`)
		args = append(args, boolInt(*filter.EnabledExplore))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// likeEscaper 转义 LIKE 通配符与转义符本身，配合 SQL 中的 ESCAPE '\' 使用。
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLike(value string) string {
	return likeEscaper.Replace(value)
}

func execUpdateOne(ctx context.Context, q queryer, query string, args ...any) error {
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func rollbackUnlessCommitted(tx *sql.Tx) {
	_ = tx.Rollback()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
