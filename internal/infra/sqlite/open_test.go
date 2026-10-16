package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAppliesPragmasOnEveryConnection(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "reader.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	// 同时持有两条连接，确保第二条是池中新建的连接。
	first, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("first conn: %v", err)
	}
	defer first.Close()
	second, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("second conn: %v", err)
	}
	defer second.Close()

	var foreignKeys, busyTimeout int
	var journalMode string
	if err := second.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if err := second.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if err := second.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if foreignKeys != 1 || busyTimeout != 5000 || journalMode != "wal" {
		t.Fatalf("foreign_keys=%d busy_timeout=%d journal_mode=%q", foreignKeys, busyTimeout, journalMode)
	}

	// 外键在新连接上生效：删除书籍级联删除章节。
	if _, err := second.ExecContext(ctx, `INSERT INTO books(book_id, book_url, name, author) VALUES ('b1', 'https://example.test/b1', 'n', 'a')`); err != nil {
		t.Fatalf("insert book: %v", err)
	}
	if _, err := second.ExecContext(ctx, `INSERT INTO chapters(book_id, book_url, url, title, chapter_index) VALUES ('b1', 'https://example.test/b1', 'https://example.test/c1', 'c1', 0)`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := second.ExecContext(ctx, `DELETE FROM books WHERE book_id = 'b1'`); err != nil {
		t.Fatalf("delete book: %v", err)
	}
	var chapters int
	if err := second.QueryRowContext(ctx, `SELECT count(*) FROM chapters WHERE book_id = 'b1'`).Scan(&chapters); err != nil {
		t.Fatalf("count chapters: %v", err)
	}
	if chapters != 0 {
		t.Fatalf("chapters after cascade delete = %d, want 0", chapters)
	}
}

func TestOpenRestrictsPermissions(t *testing.T) {
	cases := []struct {
		name     string
		existing bool
	}{
		{name: "new database", existing: false},
		{name: "existing world-readable database", existing: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "data")
			path := filepath.Join(dir, "reader.db")
			if tc.existing {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			db, err := Open(path)
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := Migrate(db); err != nil {
				t.Fatalf("Migrate() error = %v", err)
			}
			if !tc.existing {
				assertMode(t, dir, 0o700)
			}
			assertMode(t, path, 0o600)
			// WAL 旁路文件沿用主库文件权限。
			assertMode(t, path+"-wal", 0o600)
		})
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
