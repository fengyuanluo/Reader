package sqlite

import (
	"context"
	"database/sql"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// :memory: 库每条连接各自独立，固定单连接。
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func latestVersion() int64 {
	return migrations[len(migrations)-1].version
}

func schemaVersion(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var version int64
	if err := db.QueryRow(`SELECT coalesce(max(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	return version
}

func objectExists(t *testing.T, db *sql.DB, kind, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?`, kind, name).Scan(&count); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	return count > 0
}

func hasColumn(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&count); err != nil {
		t.Fatalf("query table_info: %v", err)
	}
	return count > 0
}

// assertLatestSchema 校验库已处于最新版本的结构。
func assertLatestSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{
		"book_sources",
		"books",
		"chapters",
		"replace_rules",
		"cookies",
		"source_variables",
		"cache_entries",
		"debug_sessions",
		"debug_events",
		"check_tasks",
		"check_task_items",
		"schema_migrations",
	} {
		if !objectExists(t, db, "table", table) {
			t.Fatalf("table %s missing", table)
		}
	}
	if objectExists(t, db, "table", "search_books") {
		t.Fatal("unused search_books table still exists")
	}
	if !hasColumn(t, db, "check_task_items", "failure_class") {
		t.Fatal("check_task_items.failure_class missing")
	}
	if !objectExists(t, db, "index", "idx_debug_events_session") {
		t.Fatal("idx_debug_events_session missing")
	}
	if got := schemaVersion(t, db); got != latestVersion() {
		t.Fatalf("schema version = %d, want %d", got, latestVersion())
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&rows); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if rows != len(migrations) {
		t.Fatalf("schema_migrations rows = %d, want %d", rows, len(migrations))
	}
}

func TestMigrateFreshDatabase(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	assertLatestSchema(t, db)
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	assertLatestSchema(t, db)
}

// legacyV1Schema 复刻版本化之前的迁移结果：schema_migrations 恒为 1，failure_class 由探测式补列添加。
func legacyV1Schema(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO schema_migrations(version) VALUES (1);`); err != nil {
		t.Fatalf("create legacy schema_migrations: %v", err)
	}
	if _, err := db.Exec(initialSchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
}

func TestMigrateUpgradesLegacyV1Database(t *testing.T) {
	cases := []struct {
		name         string
		failureClass bool
	}{
		{name: "failure_class already added by probe", failureClass: true},
		{name: "failure_class missing", failureClass: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			legacyV1Schema(t, db)
			if !tc.failureClass {
				if _, err := db.Exec(`ALTER TABLE check_task_items DROP COLUMN failure_class`); err != nil {
					t.Fatalf("drop failure_class: %v", err)
				}
			}
			if _, err := db.Exec(`INSERT INTO search_books(book_url, origin, name, author) VALUES ('u', 'o', 'n', 'a')`); err != nil {
				t.Fatalf("seed search_books: %v", err)
			}
			if err := Migrate(db); err != nil {
				t.Fatalf("Migrate() error = %v", err)
			}
			assertLatestSchema(t, db)
		})
	}
}

func TestApplyMigrationsRollsBackFailedStep(t *testing.T) {
	db := openTestDB(t)
	steps := []migration{
		{version: 1, name: "ok", apply: execSQL(`CREATE TABLE first_table (id INTEGER)`)},
		{version: 2, name: "broken", apply: execSQL(`CREATE TABLE second_table (id INTEGER); THIS IS NOT SQL`)},
	}
	if err := applyMigrations(context.Background(), db, steps); err == nil {
		t.Fatal("applyMigrations() error = nil, want failure")
	}
	if !objectExists(t, db, "table", "first_table") {
		t.Fatal("committed step 1 missing")
	}
	if objectExists(t, db, "table", "second_table") {
		t.Fatal("failed step 2 left a half-applied table")
	}
	if got := schemaVersion(t, db); got != 1 {
		t.Fatalf("schema version = %d, want 1", got)
	}
}

func TestBookSourceUniqueConstraints(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	_, err := db.Exec(`INSERT INTO book_sources(source_id, book_source_url, book_source_name) VALUES (?, ?, ?)`, "src_1", "https://example.test", "Example")
	if err != nil {
		t.Fatalf("insert first source: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO book_sources(source_id, book_source_url, book_source_name) VALUES (?, ?, ?)`, "src_1", "https://other.example.test", "Other"); err == nil {
		t.Fatal("duplicate source_id insert succeeded, want constraint error")
	}
	if _, err := db.Exec(`INSERT INTO book_sources(source_id, book_source_url, book_source_name) VALUES (?, ?, ?)`, "src_2", "https://example.test", "Duplicate URL"); err == nil {
		t.Fatal("duplicate book_source_url insert succeeded, want constraint error")
	}
}

func TestChapterIndexUniquePerBook(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	_, err := db.Exec(`INSERT INTO books(book_id, book_url, name, author) VALUES (?, ?, ?, ?)`, "book_1", "https://example.test/book/1", "Book", "Author")
	if err != nil {
		t.Fatalf("insert book: %v", err)
	}
	_, err = db.Exec(`INSERT INTO chapters(book_id, book_url, url, title, chapter_index) VALUES (?, ?, ?, ?, ?)`, "book_1", "https://example.test/book/1", "https://example.test/c/1", "c1", 0)
	if err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO chapters(book_id, book_url, url, title, chapter_index) VALUES (?, ?, ?, ?, ?)`, "book_1", "https://example.test/book/1", "https://example.test/c/2", "c2", 0); err == nil {
		t.Fatal("duplicate chapter index insert succeeded, want constraint error")
	}
}

// 版本 8 迁移创建 idx_cache_entries_owner，且重复迁移幂等（C-022）。
func TestMigrateCreatesCacheEntriesOwnerIndex(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if !objectExists(t, db, "index", "idx_cache_entries_owner") {
		t.Fatal("idx_cache_entries_owner missing")
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	if !objectExists(t, db, "index", "idx_cache_entries_owner") {
		t.Fatal("idx_cache_entries_owner missing after idempotent migrate")
	}
}
