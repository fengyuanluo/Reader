package repositories

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"

	"reader/internal/domain/book"
)

type ChapterRepository struct {
	db *sql.DB
}

func NewChapterRepository(db *sql.DB) *ChapterRepository {
	return &ChapterRepository{db: db}
}

func (r *ChapterRepository) ReplaceByBook(ctx context.Context, bookID string, chapters []*book.BookChapter) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx, `DELETE FROM chapters WHERE book_id = ?`, bookID); err != nil {
		return err
	}
	for _, chapter := range chapters {
		if err := chapter.Validate(); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO chapters(book_id, book_url, url, title, is_volume, base_url, chapter_index, is_vip, is_pay, resource_url, tag, word_count, variable)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			bookID, chapter.BookURL, chapter.URL, chapter.Title, boolInt(chapter.IsVolume), nullable(chapter.BaseURL), chapter.Index, boolInt(chapter.IsVIP), boolInt(chapter.IsPay), nullable(chapter.ResourceURL), nullable(chapter.Tag), nullable(chapter.WordCount), nullable(chapter.Variable)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *ChapterRepository) ListByBook(ctx context.Context, bookID string) ([]*book.BookChapter, error) {
	rows, err := r.db.QueryContext(ctx, selectChapterSQL+` WHERE book_id = ? ORDER BY chapter_index`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chapters := []*book.BookChapter{}
	for rows.Next() {
		chapter, err := scanChapter(rows)
		if err != nil {
			return nil, err
		}
		chapters = append(chapters, chapter)
	}
	return chapters, rows.Err()
}

func (r *ChapterRepository) FindByIndex(ctx context.Context, bookID string, index int) (*book.BookChapter, error) {
	return scanChapter(r.db.QueryRowContext(ctx, selectChapterSQL+` WHERE book_id = ? AND chapter_index = ?`, bookID, index))
}

func (r *ChapterRepository) SaveContent(ctx context.Context, bookID string, chapterIndex int, content string) error {
	key := contentCacheKey(bookID, chapterIndex)
	_, err := r.db.ExecContext(ctx, `
INSERT INTO cache_entries(cache_key, namespace, owner_type, owner_id, value, content_type, updated_at)
VALUES (?, 'content', 'book', ?, ?, 'text/plain; charset=utf-8', CURRENT_TIMESTAMP)
ON CONFLICT(cache_key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, key, bookID, []byte(content))
	return err
}

func (r *ChapterRepository) GetContent(ctx context.Context, bookID string, chapterIndex int) (string, bool, error) {
	key := contentCacheKey(bookID, chapterIndex)
	var raw []byte
	err := r.db.QueryRowContext(ctx, `SELECT value FROM cache_entries WHERE cache_key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(raw), true, nil
}

const selectChapterSQL = `SELECT url, title, is_volume, coalesce(base_url, ''), book_url, chapter_index, is_vip, is_pay,
coalesce(resource_url, ''), coalesce(tag, ''), coalesce(word_count, ''), coalesce(variable, '') FROM chapters`

func scanChapter(s scanner) (*book.BookChapter, error) {
	var chapter book.BookChapter
	var isVolume, isVIP, isPay int
	err := s.Scan(&chapter.URL, &chapter.Title, &isVolume, &chapter.BaseURL, &chapter.BookURL, &chapter.Index, &isVIP, &isPay, &chapter.ResourceURL, &chapter.Tag, &chapter.WordCount, &chapter.Variable)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	chapter.IsVolume = isVolume == 1
	chapter.IsVIP = isVIP == 1
	chapter.IsPay = isPay == 1
	return &chapter, nil
}

func contentCacheKey(bookID string, chapterIndex int) string {
	return "content:" + bookID + ":" + intString(chapterIndex)
}

func bookIDFromURL(bookURL string) string {
	return sourceIDLike("book_", bookURL)
}

func sourceIDLike(prefix, value string) string {
	sum := sha256Sum(value)
	return prefix + sum[:24]
}

func sha256Sum(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func intString(value int) string {
	return strconv.Itoa(value)
}
