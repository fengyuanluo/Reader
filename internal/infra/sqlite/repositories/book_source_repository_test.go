package repositories

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"reader/internal/domain/source"
	readersqlite "reader/internal/infra/sqlite"
)

// openRepoTestDB 走生产同款 Open（文件库 + 每连接外键/busy_timeout/WAL）。
func openRepoTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := readersqlite.Open(filepath.Join(t.TempDir(), "reader.db"))
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

func TestBookSourceSaveChangesURLKeepingSourceID(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	repo := NewBookSourceRepository(db)
	src := testSource("https://old.example.test", "Example")
	if err := repo.Save(ctx, src); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO source_variables(book_source_url, name, value) VALUES (?, 'token', 'v')`, src.BookSourceURL); err != nil {
		t.Fatalf("insert source variable: %v", err)
	}
	renamed := testSource("https://new.example.test", "Renamed")
	renamed.SourceID = src.SourceID
	if err := repo.Save(ctx, renamed); err != nil {
		t.Fatalf("Save(renamed) error = %v", err)
	}
	got, err := repo.FindByID(ctx, src.SourceID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if got.BookSourceURL != "https://new.example.test" || got.BookSourceName != "Renamed" {
		t.Fatalf("renamed source = %#v", got)
	}
	assertCount(t, db, `SELECT count(*) FROM book_sources`, 1)
	// 对齐 Legado：改地址等同删除旧书源，旧地址的书源变量随之清理。
	assertCount(t, db, `SELECT count(*) FROM source_variables WHERE book_source_url = ?`, 0, "https://old.example.test")
}

func TestBookSourceSaveConflicts(t *testing.T) {
	cases := []struct {
		name     string
		incoming func(a, b *source.BookSource) *source.BookSource
		wantErr  error
	}{
		{
			name: "same sourceId moves onto another source's url",
			incoming: func(a, b *source.BookSource) *source.BookSource {
				src := testSource(b.BookSourceURL, "Clash")
				src.SourceID = a.SourceID
				return src
			},
			wantErr: source.ErrConflict,
		},
		{
			name: "same url with new sourceId overwrites",
			incoming: func(a, b *source.BookSource) *source.BookSource {
				src := testSource(a.BookSourceURL, "Overwrite")
				src.SourceID = "src_custom"
				return src
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := openRepoTestDB(t)
			repo := NewBookSourceRepository(db)
			a := testSource("https://a.example.test", "A")
			b := testSource("https://b.example.test", "B")
			if err := repo.SaveMany(ctx, []*source.BookSource{a, b}); err != nil {
				t.Fatalf("SaveMany() error = %v", err)
			}
			err := repo.Save(ctx, tc.incoming(a, b))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Save() error = %v, want %v", err, tc.wantErr)
			}
			assertCount(t, db, `SELECT count(*) FROM book_sources`, 2)
		})
	}
}

func TestBookSourceSaveManyRollsBackOnConflict(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	repo := NewBookSourceRepository(db)
	a := testSource("https://a.example.test", "A")
	b := testSource("https://b.example.test", "B")
	if err := repo.SaveMany(ctx, []*source.BookSource{a, b}); err != nil {
		t.Fatalf("SaveMany() error = %v", err)
	}
	fresh := testSource("https://c.example.test", "C")
	clash := testSource(b.BookSourceURL, "Clash")
	clash.SourceID = a.SourceID
	if err := repo.SaveMany(ctx, []*source.BookSource{fresh, clash}); !errors.Is(err, source.ErrConflict) {
		t.Fatalf("SaveMany() error = %v, want ErrConflict", err)
	}
	if _, err := repo.FindByURL(ctx, fresh.BookSourceURL); !errors.Is(err, ErrNotFound) {
		t.Fatalf("source before conflict was not rolled back: %v", err)
	}
}

func TestBookSourceScanRejectsCorruptRuleJSON(t *testing.T) {
	ctx := context.Background()
	db := openRepoTestDB(t)
	repo := NewBookSourceRepository(db)
	src := testSource("https://example.test", "Example")
	if err := repo.Save(ctx, src); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := db.Exec(`UPDATE book_sources SET rule_toc = '' WHERE source_id = ?`, src.SourceID); err != nil {
		t.Fatalf("blank rule_toc: %v", err)
	}
	if _, err := repo.FindByID(ctx, src.SourceID); err != nil {
		t.Fatalf("FindByID() with empty rule column error = %v", err)
	}
	if _, err := db.Exec(`UPDATE book_sources SET rule_search = '{broken' WHERE source_id = ?`, src.SourceID); err != nil {
		t.Fatalf("corrupt rule_search: %v", err)
	}
	_, err := repo.FindByID(ctx, src.SourceID)
	if err == nil || !strings.Contains(err.Error(), "rule_search") || !strings.Contains(err.Error(), src.SourceID) {
		t.Fatalf("FindByID() error = %v, want corrupt rule_search error", err)
	}
}

func TestBookSourceListEscapesLikeWildcards(t *testing.T) {
	ctx := context.Background()
	repo := NewBookSourceRepository(openRepoTestDB(t))
	percent := testSource("https://percent.example.test", "100%好书")
	plain := testSource("https://plain.example.test", "1000好书")
	underscore := testSource("https://underscore.example.test", "U")
	underscore.BookSourceGroup = "a_b"
	lookalike := testSource("https://lookalike.example.test", "L")
	lookalike.BookSourceGroup = "axb,other"
	if err := repo.SaveMany(ctx, []*source.BookSource{percent, plain, underscore, lookalike}); err != nil {
		t.Fatalf("SaveMany() error = %v", err)
	}
	cases := []struct {
		name   string
		filter source.BookSourceFilter
		want   string
	}{
		{name: "percent keyword", filter: source.BookSourceFilter{Keyword: "100%"}, want: percent.SourceID},
		{name: "underscore keyword", filter: source.BookSourceFilter{Keyword: "a_b"}, want: underscore.SourceID},
		{name: "underscore group", filter: source.BookSourceFilter{Group: "a_b"}, want: underscore.SourceID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := repo.List(ctx, tc.filter)
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if page.Total != 1 || page.Items[0].SourceID != tc.want {
				t.Fatalf("List(%+v) = %d items, want only %s", tc.filter, page.Total, tc.want)
			}
		})
	}
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
