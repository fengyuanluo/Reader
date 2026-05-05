package repositories

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"

	"reader/internal/domain/fetch"
)

type ImageCacheRepository struct {
	db *sql.DB
}

func NewImageCacheRepository(db *sql.DB) *ImageCacheRepository {
	return &ImageCacheRepository{db: db}
}

func (r *ImageCacheRepository) GetImage(ctx context.Context, imageURL string) (fetch.ImageCacheEntry, bool, error) {
	var entry fetch.ImageCacheEntry
	err := r.db.QueryRowContext(ctx, `SELECT value, coalesce(content_type, '') FROM cache_entries WHERE cache_key = ?`, imageCacheKey(imageURL)).Scan(&entry.Body, &entry.ContentType)
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
INSERT INTO cache_entries(cache_key, namespace, owner_type, owner_id, value, content_type, updated_at)
VALUES (?, 'image', 'image', ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(cache_key) DO UPDATE SET value = excluded.value, content_type = excluded.content_type, updated_at = CURRENT_TIMESTAMP`,
		imageCacheKey(imageURL), imageURL, entry.Body, entry.ContentType)
	return err
}

func imageCacheKey(imageURL string) string {
	sum := sha256.Sum256([]byte(imageURL))
	return "image:" + hex.EncodeToString(sum[:])
}
