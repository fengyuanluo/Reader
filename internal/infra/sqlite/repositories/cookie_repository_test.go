package repositories

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"reader/internal/domain/fetch"
	readersqlite "reader/internal/infra/sqlite"

	_ "modernc.org/sqlite"
)

func TestCookieRepositorySaveListAndDelete(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := readersqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := NewCookieRepository(db)
	expires := time.Now().UTC().Truncate(time.Second)
	if err := repo.Save(ctx, fetch.Cookie{Domain: "example.test", Name: "sid", Value: "abc", Path: "/", ExpiresAt: &expires}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	cookies, err := repo.ListByDomain(ctx, "example.test")
	if err != nil {
		t.Fatalf("ListByDomain() error = %v", err)
	}
	if len(cookies) != 1 || cookies[0].Name != "sid" || cookies[0].Value != "abc" || cookies[0].ExpiresAt == nil {
		t.Fatalf("cookies = %#v", cookies)
	}
	if err := repo.Delete(ctx, "example.test", "sid"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	cookies, err = repo.ListByDomain(ctx, "example.test")
	if err != nil {
		t.Fatalf("ListByDomain after delete error = %v", err)
	}
	if len(cookies) != 0 {
		t.Fatalf("cookies after delete = %#v", cookies)
	}
}

func TestCookieRepositoryTreatsCorruptExpiresAsExpired(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := readersqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cookies(domain, name, value, expires_at, updated_at) VALUES ('example.test', 'sid', 'abc', 'not-a-time', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	cookies, err := NewCookieRepository(db).ListByDomain(ctx, "example.test")
	if err != nil {
		t.Fatalf("ListByDomain() error = %v", err)
	}
	if len(cookies) != 1 || cookies[0].ExpiresAt == nil || cookies[0].ExpiresAt.After(time.Now()) {
		t.Fatalf("cookies = %#v, want corrupt expires_at treated as expired", cookies)
	}
}

// TestCookieRepositoryListByDomainFiltersExpired 覆盖 S11-10：已过期 cookie 不再返回，
// 未过期与无 expires_at（会话）的 cookie 正常返回。
func TestCookieRepositoryListByDomainFiltersExpired(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := readersqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := NewCookieRepository(db)
	past := time.Now().UTC().Add(-time.Hour)
	future := time.Now().UTC().Add(time.Hour)
	if err := repo.Save(ctx, fetch.Cookie{Domain: "example.test", Name: "expired", Value: "e", ExpiresAt: &past}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := repo.Save(ctx, fetch.Cookie{Domain: "example.test", Name: "fresh", Value: "f", ExpiresAt: &future}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := repo.Save(ctx, fetch.Cookie{Domain: "example.test", Name: "session", Value: "s"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	cookies, err := repo.ListByDomain(ctx, "example.test")
	if err != nil {
		t.Fatalf("ListByDomain() error = %v", err)
	}
	names := map[string]bool{}
	for _, cookie := range cookies {
		names[cookie.Name] = true
	}
	if len(cookies) != 2 || !names["fresh"] || !names["session"] || names["expired"] {
		t.Fatalf("cookies = %#v, want only fresh and session", cookies)
	}
}
