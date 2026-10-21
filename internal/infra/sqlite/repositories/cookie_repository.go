package repositories

import (
	"context"
	"database/sql"
	"time"

	"reader/internal/domain/fetch"
)

type CookieRepository struct {
	db *sql.DB
}

func NewCookieRepository(db *sql.DB) *CookieRepository {
	return &CookieRepository{db: db}
}

func (r *CookieRepository) Save(ctx context.Context, cookie fetch.Cookie) error {
	var expires any
	if cookie.ExpiresAt != nil {
		expires = cookie.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO cookies(domain, name, value, path, expires_at, updated_at)
VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(domain, name) DO UPDATE SET
  value = excluded.value,
  path = excluded.path,
  expires_at = excluded.expires_at,
  updated_at = CURRENT_TIMESTAMP`,
		cookie.Domain, cookie.Name, cookie.Value, nullable(cookie.Path), expires)
	return err
}

func (r *CookieRepository) ListByDomain(ctx context.Context, domain string) ([]fetch.Cookie, error) {
	// 只返回未过期的 Cookie（S11-10）：expires_at 为 NULL 视为不过期，
	// 以 RFC3339Nano UTC 文本比较，与写入格式一致。
	rows, err := r.db.QueryContext(ctx, `
SELECT domain, name, value, coalesce(path, ''), expires_at FROM cookies
WHERE domain = ? AND (expires_at IS NULL OR expires_at > ?)
ORDER BY name ASC`, domain, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []fetch.Cookie{}
	for rows.Next() {
		var cookie fetch.Cookie
		var expires sql.NullString
		if err := rows.Scan(&cookie.Domain, &cookie.Name, &cookie.Value, &cookie.Path, &expires); err != nil {
			return nil, err
		}
		if expires.Valid {
			parsed, err := time.Parse(time.RFC3339Nano, expires.String)
			if err != nil {
				// expires_at 损坏时按已过期处理（零值时间），由调用方丢弃并清理，不能视为永不过期。
				parsed = time.Time{}
			}
			cookie.ExpiresAt = &parsed
		}
		out = append(out, cookie)
	}
	return out, rows.Err()
}

func (r *CookieRepository) Delete(ctx context.Context, domain string, name string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM cookies WHERE domain = ? AND name = ?`, domain, name)
	return err
}

// DeleteExpired 一次性删除 domain 下早于 before 的 Cookie（S08-07）：
// 单条 SQL 一次往返，返回删除行数。供 fetcher.CookieManager 通过可选接口类型断言调用。
func (r *CookieRepository) DeleteExpired(ctx context.Context, domain string, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM cookies WHERE domain = ? AND expires_at IS NOT NULL AND expires_at <= ?`,
		domain, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
