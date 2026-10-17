package repositories

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"

	"reader/internal/domain/fetch"
)

// imageCacheTTL 是图片缓存有效期（SQLite datetime 修饰符）。expires_at 以 SQLite datetime
// 定宽格式存储，才能按文本正确比较大小。
const imageCacheTTL = "+7 days"

type ImageCacheRepository struct {
	db *sql.DB
}

func NewImageCacheRepository(db *sql.DB) *ImageCacheRepository {
	return &ImageCacheRepository{db: db}
}

func (r *ImageCacheRepository) GetImage(ctx context.Context, imageURL string) (fetch.ImageCacheEntry, bool, error) {
	var entry fetch.ImageCacheEntry
	err := r.db.QueryRowContext(ctx, `SELECT value, coalesce(content_type, '') FROM cache_entries
WHERE cache_key = ? AND expires_at > datetime('now')`, imageCacheKey(imageURL)).Scan(&entry.Body, &entry.ContentType)
	if errors.Is(err, sql.ErrNoRows) {
		return fetch.ImageCacheEntry{}, false, nil
	}
	if err != nil {
		return fetch.ImageCacheEntry{}, false, err
	}
	return entry, true, nil
}

func (r *ImageCacheRepository) SaveImage(ctx context.Context, imageURL string, entry fetch.ImageCacheEntry) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO cache_entries(cache_key, namespace, owner_type, owner_id, value, content_type, expires_at, updated_at)
VALUES (?, 'image', 'image', ?, ?, ?, datetime('now', ?), CURRENT_TIMESTAMP)
ON CONFLICT(cache_key) DO UPDATE SET value = excluded.value, content_type = excluded.content_type, expires_at = excluded.expires_at, updated_at = CURRENT_TIMESTAMP`,
		imageCacheKey(imageURL), imageURL, entry.Body, entry.ContentType, imageCacheTTL)
	return err
}

// DeleteExpired 删除已过期的图片缓存；expires_at 为空的是引入 TTL 之前写入的旧条目，一并视为过期。
// 先用 LIMIT 1 短路查询：无过期条目时避免执行 DELETE 的写事务开销（启动清理热路径）。
func (r *ImageCacheRepository) DeleteExpired(ctx context.Context) (int64, error) {
	var exists int
	if err := r.db.QueryRowContext(ctx, `SELECT 1 FROM cache_entries
WHERE namespace = 'image' AND (expires_at IS NULL OR expires_at <= datetime('now')) LIMIT 1`).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM cache_entries
WHERE namespace = 'image' AND (expires_at IS NULL OR expires_at <= datetime('now'))`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func imageCacheKey(imageURL string) string {
	sum := sha256.Sum256([]byte(imageURL))
	return "image:" + hex.EncodeToString(sum[:])
}
