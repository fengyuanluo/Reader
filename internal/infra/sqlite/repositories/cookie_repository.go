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
	rows, err := r.db.QueryContext(ctx, `SELECT domain, name, value, coalesce(path, ''), expires_at FROM cookies WHERE domain = ? ORDER BY name ASC`, domain)
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
			if err == nil {
				cookie.ExpiresAt = &parsed
			}
		}
		out = append(out, cookie)
	}
	return out, rows.Err()
}

func (r *CookieRepository) Delete(ctx context.Context, domain string, name string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM cookies WHERE domain = ? AND name = ?`, domain, name)
	return err
}
