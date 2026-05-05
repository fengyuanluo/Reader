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
