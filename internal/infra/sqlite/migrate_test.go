package sqlite

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestMigrateCreatesRequiredTables(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	for _, table := range []string{
		"book_sources",
		"books",
		"search_books",
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
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}

	var column string
	if err := db.QueryRow(`SELECT name FROM pragma_table_info('check_task_items') WHERE name = 'failure_class'`).Scan(&column); err != nil {
		t.Fatalf("check_task_items failure_class column missing: %v", err)
	}
}

func TestMigrateSetsBusyTimeout(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	var timeout int
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if timeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", timeout)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	version, err := SchemaVersion(db)
	if err != nil {
		t.Fatalf("SchemaVersion() error = %v", err)
	}
	if version != InitialSchemaVersion() {
		t.Fatalf("schema version = %d, want %d", version, InitialSchemaVersion())
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
