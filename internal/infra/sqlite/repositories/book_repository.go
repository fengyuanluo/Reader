package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

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
		_ = json.Unmarshal([]byte(rawDownloadURLs), &b.DownloadURLs)
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
