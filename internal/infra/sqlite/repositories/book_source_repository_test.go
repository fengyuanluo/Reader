package repositories

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"reader/internal/domain/source"
	readersqlite "reader/internal/infra/sqlite"

	_ "modernc.org/sqlite"
)

func openRepoTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := readersqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestBookSourceRepositoryCRUD(t *testing.T) {
	ctx := context.Background()
	repo := NewBookSourceRepository(openRepoTestDB(t))
	src := testSource("https://example.test", "Example")

	if err := repo.Save(ctx, src); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	byID, err := repo.FindByID(ctx, src.SourceID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if byID.BookSourceURL != src.BookSourceURL || byID.BookSourceName != src.BookSourceName {
		t.Fatalf("FindByID() = %#v", byID)
	}
	byURL, err := repo.FindByURL(ctx, src.BookSourceURL)
	if err != nil {
		t.Fatalf("FindByURL() error = %v", err)
	}
	if byURL.SourceID != src.SourceID {
		t.Fatalf("FindByURL SourceID = %q, want %q", byURL.SourceID, src.SourceID)
	}
}

func TestBookSourceRepositoryListFilters(t *testing.T) {
	ctx := context.Background()
	repo := NewBookSourceRepository(openRepoTestDB(t))
	first := testSource("https://one.example.test", "Alpha")
	first.BookSourceGroup = "default,group-a"
	first.BookSourceComment = "fast source"
	first.Enabled = true
	first.EnabledExplore = false
	second := testSource("https://two.example.test", "Beta")
	second.BookSourceGroup = "group-b"
	second.Enabled = false
	second.EnabledExplore = true
	if err := repo.SaveMany(ctx, []*source.BookSource{first, second}); err != nil {
		t.Fatalf("SaveMany() error = %v", err)
	}

	enabled := true
	page, err := repo.List(ctx, source.BookSourceFilter{Keyword: "fast", Group: "group-a", Enabled: &enabled, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].SourceID != first.SourceID {
		t.Fatalf("filtered page = %#v", page)
	}

	explore := true
	page, err = repo.List(ctx, source.BookSourceFilter{EnabledExplore: &explore, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List explore enabled error = %v", err)
	}
	if page.Total != 1 || page.Items[0].SourceID != second.SourceID {
		t.Fatalf("explore filtered page = %#v", page)
	}
}

func TestBookSourceRepositoryUpdateAndReorder(t *testing.T) {
	ctx := context.Background()
	repo := NewBookSourceRepository(openRepoTestDB(t))
	src := testSource("https://example.test", "Example")
	if err := repo.Save(ctx, src); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if err := repo.UpdateEnabled(ctx, src.SourceID, false); err != nil {
		t.Fatalf("UpdateEnabled() error = %v", err)
	}
	if err := repo.UpdateExploreEnabled(ctx, src.SourceID, false); err != nil {
		t.Fatalf("UpdateExploreEnabled() error = %v", err)
	}
	if err := repo.Reorder(ctx, []source.SourceOrder{{SourceID: src.SourceID, CustomOrder: 42}}); err != nil {
		t.Fatalf("Reorder() error = %v", err)
	}

	got, err := repo.FindByID(ctx, src.SourceID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if got.Enabled || got.EnabledExplore || got.CustomOrder != 42 {
		t.Fatalf("updated source = %#v", got)
	}
}

func TestBookSourceRepositoryDeleteCleansSourceData(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	repo := NewBookSourceRepository(db)
	src := testSource("https://example.test", "Example")
	if err := repo.Save(ctx, src); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO source_variables(book_source_url, name, value) VALUES (?, ?, ?)`, src.BookSourceURL, "token", "secret"); err != nil {
		t.Fatalf("insert source variable: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO cache_entries(cache_key, namespace, owner_type, owner_id, value) VALUES (?, ?, ?, ?, ?)`, "cache-1", "source", "source", src.BookSourceURL, "value"); err != nil {
		t.Fatalf("insert cache: %v", err)
	}

	if err := repo.Delete(ctx, src.SourceID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repo.FindByID(ctx, src.SourceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FindByID after delete error = %v, want ErrNotFound", err)
	}
	assertCount(t, db, `SELECT count(*) FROM source_variables WHERE book_source_url = ?`, 0, src.BookSourceURL)
	assertCount(t, db, `SELECT count(*) FROM cache_entries WHERE owner_type = 'source' AND owner_id = ?`, 0, src.BookSourceURL)
}

func testSource(url, name string) *source.BookSource {
	src := &source.BookSource{
		BookSourceURL:    url,
		BookSourceName:   name,
		BookSourceType:   0,
		CustomOrder:      1,
		Enabled:          true,
		EnabledExplore:   true,
		EnabledCookieJar: true,
		SearchURL:        url + "/search",
	}
	if err := src.Normalize(); err != nil {
		panic(err)
	}
	return src
}

func assertCount(t *testing.T, db *sql.DB, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := db.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatalf("count query error: %v", err)
	}
	if got != want {
		t.Fatalf("count = %d, want %d", got, want)
	}
}
