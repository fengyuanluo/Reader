package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"reader/internal/domain/book"
)

type BookRepository struct {
	db *sql.DB
}

func NewBookRepository(db *sql.DB) *BookRepository {
	return &BookRepository{db: db}
}

func (r *BookRepository) Save(ctx context.Context, b *book.Book) error {
	_, err := r.db.ExecContext(ctx, `
	INSERT INTO books (
	  book_id, book_url, toc_url, origin, origin_name, name, author, kind, custom_tag, cover_url, custom_cover_url,
	  intro, custom_intro, charset, type, book_group, word_count, latest_chapter_title, latest_chapter_time,
	  last_check_time, last_check_count, total_chapter_num, info_html, toc_html, download_urls, dur_chapter_title,
	  dur_chapter_index, dur_chapter_pos, dur_chapter_time, can_update, book_order, origin_order, variable, sync_time, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(book_url) DO UPDATE SET
	  toc_url = excluded.toc_url,
	  origin = excluded.origin,
	  origin_name = excluded.origin_name,
	  name = excluded.name,
	  author = excluded.author,
	  kind = excluded.kind,
	  custom_tag = excluded.custom_tag,
	  cover_url = excluded.cover_url,
	  custom_cover_url = excluded.custom_cover_url,
	  intro = excluded.intro,
	  custom_intro = excluded.custom_intro,
	  charset = excluded.charset,
	  type = excluded.type,
	  book_group = excluded.book_group,
	  word_count = excluded.word_count,
	  latest_chapter_title = excluded.latest_chapter_title,
	  latest_chapter_time = excluded.latest_chapter_time,
	  last_check_time = excluded.last_check_time,
	  last_check_count = excluded.last_check_count,
	  total_chapter_num = excluded.total_chapter_num,
	  info_html = excluded.info_html,
	  toc_html = excluded.toc_html,
	  download_urls = excluded.download_urls,
	  dur_chapter_title = excluded.dur_chapter_title,
	  dur_chapter_index = excluded.dur_chapter_index,
	  dur_chapter_pos = excluded.dur_chapter_pos,
	  dur_chapter_time = excluded.dur_chapter_time,
	  can_update = excluded.can_update,
	  book_order = excluded.book_order,
	  origin_order = excluded.origin_order,
	  variable = excluded.variable,
	  sync_time = excluded.sync_time,
	  updated_at = CURRENT_TIMESTAMP`,
		bookIDFromURL(b.BookURL), b.BookURL, nullable(b.TocURL), nullable(b.Origin), nullable(b.OriginName), b.Name, b.Author, nullable(b.Kind),
		nullable(b.CustomTag), nullable(b.CoverURL), nullable(b.CustomCoverURL), nullable(b.Intro), nullable(b.CustomIntro), nullable(b.Charset),
		b.Type, b.Group, nullable(b.WordCount), nullable(b.LatestChapterTitle), b.LatestChapterTime, b.LastCheckTime, b.LastCheckCount,
		b.TotalChapterNum, nullable(b.InfoHTML), nullable(b.TocHTML), nullable(mustJSONString(b.DownloadURLs)), nullable(b.DurChapterTitle),
		b.DurChapterIndex, b.DurChapterPos, b.DurChapterTime, boolInt(b.CanUpdate), b.Order, b.OriginOrder, nullable(b.Variable), b.SyncTime,
	)
	return err
}

// UpdateInfo 只更新书籍信息刷新（RefreshInfo）需要写入的列，避免 RefreshInfo 路径上的
// 重复整行写覆盖阅读进度等无关列；行不存在时不创建。
func (r *BookRepository) UpdateInfo(ctx context.Context, b *book.Book) error {
	result, err := r.db.ExecContext(ctx, `
	UPDATE books SET
	  toc_url = ?, origin = ?, origin_name = ?, name = ?, author = ?, kind = ?, cover_url = ?, intro = ?,
	  type = ?, word_count = ?, latest_chapter_title = ?, total_chapter_num = ?, info_html = ?, toc_html = ?,
	  download_urls = ?, origin_order = ?, variable = ?, updated_at = CURRENT_TIMESTAMP
	WHERE book_url = ?`,
		nullable(b.TocURL), nullable(b.Origin), nullable(b.OriginName), b.Name, b.Author, nullable(b.Kind),
		nullable(b.CoverURL), nullable(b.Intro), b.Type, nullable(b.WordCount), nullable(b.LatestChapterTitle),
		b.TotalChapterNum, nullable(b.InfoHTML), nullable(b.TocHTML), nullable(mustJSONString(b.DownloadURLs)),
		b.OriginOrder, nullable(b.Variable), b.BookURL)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: %w", book.ErrNotFound, ErrNotFound)
	}
	return nil
}

func (r *BookRepository) FindByID(ctx context.Context, id string) (*book.Book, error) {
	return scanBook(r.db.QueryRowContext(ctx, selectBookSQL+` WHERE book_id = ?`, id))
}

// UpdateTocStats 只更新目录刷新（RefreshToc）相关列（S12-01/S12-03/S12-05）：
// Variable 仅在 VariableChanged 时写入，dur_* 仅在 DurChapterChanged 时写入；
// 行不存在时不创建，返回 ErrNotFound 包装（与 UpdateInfo 一致）。
func (r *BookRepository) UpdateTocStats(ctx context.Context, bookID string, stats book.TocStats) error {
	setClauses := []string{
		`total_chapter_num = ?`, `last_check_time = ?`, `last_check_count = ?`,
		`latest_chapter_time = ?`, `latest_chapter_title = ?`,
	}
	args := []any{stats.TotalChapterNum, stats.LastCheckTime, stats.LastCheckCount, stats.LatestChapterTime, nullable(stats.LatestChapterTitle)}
	if stats.VariableChanged {
		setClauses = append(setClauses, `variable = ?`)
		args = append(args, nullable(stats.Variable))
	}
	if stats.DurChapterChanged {
		setClauses = append(setClauses, `dur_chapter_title = ?`, `dur_chapter_index = ?`, `dur_chapter_pos = ?`, `dur_chapter_time = ?`)
		args = append(args, nullable(stats.DurChapterTitle), stats.DurChapterIndex, stats.DurChapterPos, stats.DurChapterTime)
	}
	args = append(args, bookID)
	query := `UPDATE books SET ` + strings.Join(setClauses, ", ") + `, updated_at = CURRENT_TIMESTAMP WHERE book_id = ?`
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: %w", book.ErrNotFound, ErrNotFound)
	}
	return nil
}

func (r *BookRepository) FindByURL(ctx context.Context, bookURL string) (*book.Book, error) {
	return scanBook(r.db.QueryRowContext(ctx, selectBookSQL+` WHERE book_url = ?`, bookURL))
}

func (r *BookRepository) List(ctx context.Context, filter book.BookFilter) ([]*book.Book, int, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize < 1 {
		pageSize = 50
	}
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM books`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, selectBookSQL+` ORDER BY dur_chapter_time DESC LIMIT ? OFFSET ?`, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	books := []*book.Book{}
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, 0, err
		}
		books = append(books, b)
	}
	return books, total, rows.Err()
}

func (r *BookRepository) Delete(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	if err := execUpdateOne(ctx, tx, `DELETE FROM books WHERE book_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cache_entries WHERE owner_type = 'book' AND owner_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *BookRepository) SaveProgress(ctx context.Context, id string, progress book.BookProgress) error {
	return execUpdateOne(ctx, r.db, `UPDATE books SET dur_chapter_title = ?, dur_chapter_index = ?, dur_chapter_pos = ?, dur_chapter_time = ?, updated_at = CURRENT_TIMESTAMP WHERE book_id = ?`,
		nullable(progress.DurChapterTitle), progress.DurChapterIndex, progress.DurChapterPos, progress.DurChapterTime, id)
}

const selectBookSQL = `SELECT book_url, coalesce(toc_url, ''), coalesce(origin, ''), coalesce(origin_name, ''),
name, author, coalesce(kind, ''), coalesce(custom_tag, ''), coalesce(cover_url, ''), coalesce(custom_cover_url, ''),
coalesce(intro, ''), coalesce(custom_intro, ''), coalesce(charset, ''), type, book_group, coalesce(word_count, ''),
coalesce(latest_chapter_title, ''), latest_chapter_time, last_check_time, last_check_count, total_chapter_num,
coalesce(info_html, ''), coalesce(toc_html, ''), coalesce(download_urls, ''),
coalesce(dur_chapter_title, ''), dur_chapter_index, dur_chapter_pos, dur_chapter_time, can_update, book_order, origin_order,
coalesce(variable, ''), sync_time FROM books`

// selectBookMetaSQL 是书架列表投影（S11-01）：书架列表与 OpenAPI 的书架端点只消费
// 元数据列，info_html/toc_html（可达数百 KB/本）与 download_urls 大列不参与列表响应，
// 全列扫描会在书架大时放大 IO 与内存。大列仅详情路径（FindByID/FindByURL）读取。
// 列序与 scanBookMeta 一致。
const selectBookMetaSQL = `SELECT book_url, coalesce(toc_url, ''), coalesce(origin, ''), coalesce(origin_name, ''),
name, author, coalesce(kind, ''), coalesce(custom_tag, ''), coalesce(cover_url, ''), coalesce(custom_cover_url, ''),
coalesce(intro, ''), coalesce(custom_intro, ''), coalesce(charset, ''), type, book_group, coalesce(word_count, ''),
coalesce(latest_chapter_title, ''), latest_chapter_time, last_check_time, last_check_count, total_chapter_num,
coalesce(dur_chapter_title, ''), dur_chapter_index, dur_chapter_pos, dur_chapter_time, can_update, book_order, origin_order,
coalesce(variable, ''), sync_time FROM books`

// scanBookMeta 按 selectBookMetaSQL 的列序扫描书籍元数据行。
func scanBookMeta(s scanner) (*book.Book, error) {
	var b book.Book
	var canUpdate int
	err := s.Scan(&b.BookURL, &b.TocURL, &b.Origin, &b.OriginName, &b.Name, &b.Author, &b.Kind, &b.CustomTag, &b.CoverURL, &b.CustomCoverURL,
		&b.Intro, &b.CustomIntro, &b.Charset, &b.Type, &b.Group, &b.WordCount,
		&b.LatestChapterTitle, &b.LatestChapterTime, &b.LastCheckTime, &b.LastCheckCount, &b.TotalChapterNum,
		&b.DurChapterTitle, &b.DurChapterIndex, &b.DurChapterPos, &b.DurChapterTime, &canUpdate, &b.Order, &b.OriginOrder,
		&b.Variable, &b.SyncTime)
	if errors.Is(err, sql.ErrNoRows) {
		// 同时包裹领域哨兵 book.ErrNotFound 与仓储层通用 ErrNotFound：调用方按所在域用 errors.Is 判定。
		return nil, fmt.Errorf("%w: %w", book.ErrNotFound, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	b.CanUpdate = canUpdate == 1
	return &b, nil
}

// ListMeta 是书架列表的投影查询（S11-01）：与 List 的过滤和分页语义一致，但不读取
// info_html/toc_html/download_urls 大列。供列表/书架场景调用方按需使用；List 保持
// 全量行为不变（ListAll 调用方等需要完整规则的场景继续用 List）。
func (r *BookRepository) ListMeta(ctx context.Context, filter book.BookFilter) ([]*book.Book, int, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize < 1 {
		pageSize = 50
	}
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM books`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, selectBookMetaSQL+` ORDER BY dur_chapter_time DESC LIMIT ? OFFSET ?`, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	books := []*book.Book{}
	for rows.Next() {
		b, err := scanBookMeta(rows)
		if err != nil {
			return nil, 0, err
		}
		books = append(books, b)
	}
	return books, total, rows.Err()
}

func scanBook(s scanner) (*book.Book, error) {
	var b book.Book
	var rawDownloadURLs string
	var canUpdate int
	err := s.Scan(&b.BookURL, &b.TocURL, &b.Origin, &b.OriginName, &b.Name, &b.Author, &b.Kind, &b.CustomTag, &b.CoverURL, &b.CustomCoverURL,
		&b.Intro, &b.CustomIntro, &b.Charset, &b.Type, &b.Group, &b.WordCount,
		&b.LatestChapterTitle, &b.LatestChapterTime, &b.LastCheckTime, &b.LastCheckCount, &b.TotalChapterNum,
		&b.InfoHTML, &b.TocHTML, &rawDownloadURLs,
		&b.DurChapterTitle, &b.DurChapterIndex, &b.DurChapterPos, &b.DurChapterTime, &canUpdate, &b.Order, &b.OriginOrder,
		&b.Variable, &b.SyncTime)
	if errors.Is(err, sql.ErrNoRows) {
		// 同时包裹领域哨兵 book.ErrNotFound 与仓储层通用 ErrNotFound：调用方按所在域用 errors.Is 判定。
		return nil, fmt.Errorf("%w: %w", book.ErrNotFound, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	b.CanUpdate = canUpdate == 1
	if rawDownloadURLs != "" {
		// 与 book_source 侧一致：download_urls 损坏时报错而不是静默得到 nil
		// （S11-06），否则下载导出静默缺数据且无法定位。
		if err := json.Unmarshal([]byte(rawDownloadURLs), &b.DownloadURLs); err != nil {
			return nil, fmt.Errorf("书籍 %s 的 download_urls 列 JSON 损坏: %w", b.BookURL, err)
		}
	}
	return &b, nil
}

func mustJSONString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(raw)
}
