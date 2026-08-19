package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"reader/internal/domain/source"
)

var ErrNotFound = source.ErrNotFound

type BookSourceRepository struct {
	db *sql.DB
}

func NewBookSourceRepository(db *sql.DB) *BookSourceRepository {
	return &BookSourceRepository{db: db}
}

func (r *BookSourceRepository) Save(ctx context.Context, src *source.BookSource) error {
	if err := src.Normalize(); err != nil {
		return err
	}
	return r.save(ctx, r.db, src)
}

func (r *BookSourceRepository) SaveMany(ctx context.Context, sources []*source.BookSource) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	for _, src := range sources {
		if err := src.Normalize(); err != nil {
			return err
		}
		if err := r.save(ctx, tx, src); err != nil {
			return err
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM source_variables WHERE book_source_url = ?`, src.BookSourceURL); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cache_entries WHERE owner_type = 'source' AND owner_id = ?`, src.BookSourceURL); err != nil {
		return err
	}
	return tx.Commit()
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

func (r *BookSourceRepository) save(ctx context.Context, q queryer, src *source.BookSource) error {
	ruleExplore, _ := json.Marshal(src.RuleExplore)
	ruleSearch, _ := json.Marshal(src.RuleSearch)
	ruleBookInfo, _ := json.Marshal(src.RuleBookInfo)
	ruleToc, _ := json.Marshal(src.RuleToc)
	ruleContent, _ := json.Marshal(src.RuleContent)
	_, err := q.ExecContext(ctx, `
INSERT INTO book_sources (
  source_id, book_source_url, book_source_name, book_source_group, book_source_type, book_url_pattern,
  custom_order, enabled, enabled_explore, event_listener, custom_button, js_lib, enabled_cookie_jar, concurrent_rate, header, login_url,
  login_ui, login_check_js, cover_decode_js, book_source_comment, variable_comment, last_update_time,
  respond_time, weight, explore_url, explore_screen, rule_explore, search_url, rule_search, rule_book_info,
  rule_toc, rule_content, rule_review, raw_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(book_source_url) DO UPDATE SET
  source_id = excluded.source_id,
  book_source_name = excluded.book_source_name,
  book_source_group = excluded.book_source_group,
  book_source_type = excluded.book_source_type,
  book_url_pattern = excluded.book_url_pattern,
  custom_order = excluded.custom_order,
  enabled = excluded.enabled,
  enabled_explore = excluded.enabled_explore,
  event_listener = excluded.event_listener,
  custom_button = excluded.custom_button,
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
  updated_at = CURRENT_TIMESTAMP`,
		src.SourceID, src.BookSourceURL, src.BookSourceName, nullable(src.BookSourceGroup), src.BookSourceType, nullable(src.BookURLPattern),
		src.CustomOrder, boolInt(src.Enabled), boolInt(src.EnabledExplore), boolInt(src.EventListener), boolInt(src.CustomButton), nullable(src.JSLib), boolInt(src.EnabledCookieJar), nullable(src.ConcurrentRate), nullable(src.Header), nullable(src.LoginURL),
		nullable(src.LoginUI), nullable(src.LoginCheckJS), nullable(src.CoverDecodeJS), nullable(src.BookSourceComment), nullable(src.VariableComment), src.LastUpdateTime,
		src.RespondTime, src.Weight, nullable(src.ExploreURL), nullable(src.ExploreScreen), string(ruleExplore), nullable(src.SearchURL), string(ruleSearch), string(ruleBookInfo),
		string(ruleToc), string(ruleContent), string(src.RuleReview), string(src.RawJSON),
	)
	return err
}

const selectBookSourceSQL = `SELECT source_id, book_source_url, book_source_name, coalesce(book_source_group, ''),
book_source_type, coalesce(book_url_pattern, ''), custom_order, enabled, enabled_explore, event_listener, custom_button, coalesce(js_lib, ''),
enabled_cookie_jar, coalesce(concurrent_rate, ''), coalesce(header, ''), coalesce(login_url, ''), coalesce(login_ui, ''),
coalesce(login_check_js, ''), coalesce(cover_decode_js, ''), coalesce(book_source_comment, ''), coalesce(variable_comment, ''),
last_update_time, respond_time, weight, coalesce(explore_url, ''), coalesce(explore_screen, ''), coalesce(rule_explore, 'null'),
coalesce(search_url, ''), coalesce(rule_search, 'null'), coalesce(rule_book_info, 'null'), coalesce(rule_toc, 'null'),
coalesce(rule_content, 'null'), coalesce(rule_review, ''), coalesce(raw_json, '') FROM book_sources`

type scanner interface {
	Scan(dest ...any) error
}

func scanBookSource(s scanner) (*source.BookSource, error) {
	var src source.BookSource
	var enabled, enabledExplore, eventListener, customButton, enabledCookieJar int
	var ruleExplore, ruleSearch, ruleBookInfo, ruleToc, ruleContent, ruleReview, rawJSON string
	err := s.Scan(
		&src.SourceID, &src.BookSourceURL, &src.BookSourceName, &src.BookSourceGroup,
		&src.BookSourceType, &src.BookURLPattern, &src.CustomOrder, &enabled, &enabledExplore, &eventListener, &customButton, &src.JSLib,
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
	src.EventListener = eventListener == 1
	src.CustomButton = customButton == 1
	src.EnabledCookieJar = enabledCookieJar == 1
	_ = json.Unmarshal([]byte(ruleExplore), &src.RuleExplore)
	_ = json.Unmarshal([]byte(ruleSearch), &src.RuleSearch)
	_ = json.Unmarshal([]byte(ruleBookInfo), &src.RuleBookInfo)
	_ = json.Unmarshal([]byte(ruleToc), &src.RuleToc)
	_ = json.Unmarshal([]byte(ruleContent), &src.RuleContent)
	src.RuleReview = []byte(ruleReview)
	src.RawJSON = []byte(rawJSON)
	return &src, nil
}

func buildBookSourceWhere(filter source.BookSourceFilter) (string, []any) {
	clauses := []string{}
	args := []any{}
	if filter.Keyword != "" {
		clauses = append(clauses, `(book_source_name LIKE ? OR book_source_group LIKE ? OR book_source_url LIKE ? OR book_source_comment LIKE ?)`)
		like := "%" + filter.Keyword + "%"
		args = append(args, like, like, like, like)
	}
	if filter.Group != "" {
		clauses = append(clauses, `(book_source_group = ? OR book_source_group LIKE ? OR book_source_group LIKE ? OR book_source_group LIKE ?)`)
		args = append(args, filter.Group, filter.Group+",%", "%,"+filter.Group, "%,"+filter.Group+",%")
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
