package sqlite

import (
	"database/sql"
	"fmt"
)

const initialSchemaVersion int64 = 1

func Migrate(db *sql.DB) error {
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("enable busy timeout: %w", err)
	}
	if _, err := db.Exec(initialSchema); err != nil {
		return fmt.Errorf("apply initial schema: %w", err)
	}
	if err := ensureColumn(db, "check_task_items", "failure_class", `ALTER TABLE check_task_items ADD COLUMN failure_class TEXT`); err != nil {
		return fmt.Errorf("ensure check_task_items.failure_class: %w", err)
	}
	return nil
}

const initialSchema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (1);

CREATE TABLE IF NOT EXISTS book_sources (
  source_id TEXT NOT NULL UNIQUE,
  book_source_url TEXT NOT NULL UNIQUE,
  book_source_name TEXT NOT NULL,
  book_source_group TEXT,
  book_source_type INTEGER NOT NULL DEFAULT 0,
  book_url_pattern TEXT,
  custom_order INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  enabled_explore INTEGER NOT NULL DEFAULT 1,
  js_lib TEXT,
  enabled_cookie_jar INTEGER NOT NULL DEFAULT 1,
  concurrent_rate TEXT,
  header TEXT,
  login_url TEXT,
  login_ui TEXT,
  login_check_js TEXT,
  cover_decode_js TEXT,
  book_source_comment TEXT,
  variable_comment TEXT,
  last_update_time INTEGER NOT NULL DEFAULT 0,
  respond_time INTEGER NOT NULL DEFAULT 180000,
  weight INTEGER NOT NULL DEFAULT 0,
  explore_url TEXT,
  explore_screen TEXT,
  rule_explore TEXT,
  search_url TEXT,
  rule_search TEXT,
  rule_book_info TEXT,
  rule_toc TEXT,
  rule_content TEXT,
  rule_review TEXT,
  raw_json TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_book_sources_enabled ON book_sources(enabled);
CREATE INDEX IF NOT EXISTS idx_book_sources_enabled_explore ON book_sources(enabled_explore);
CREATE INDEX IF NOT EXISTS idx_book_sources_custom_order ON book_sources(custom_order);

CREATE TABLE IF NOT EXISTS books (
  book_id TEXT NOT NULL PRIMARY KEY,
  book_url TEXT NOT NULL UNIQUE,
  toc_url TEXT,
  origin TEXT,
  origin_name TEXT,
  name TEXT NOT NULL,
  author TEXT NOT NULL,
  kind TEXT,
  cover_url TEXT,
  intro TEXT,
  type INTEGER NOT NULL DEFAULT 0,
  word_count TEXT,
  latest_chapter_title TEXT,
  info_html TEXT,
  toc_html TEXT,
  download_urls TEXT,
  latest_chapter_time INTEGER NOT NULL DEFAULT 0,
  total_chapter_num INTEGER NOT NULL DEFAULT 0,
  dur_chapter_title TEXT,
  dur_chapter_index INTEGER NOT NULL DEFAULT 0,
  dur_chapter_pos INTEGER NOT NULL DEFAULT 0,
  dur_chapter_time INTEGER NOT NULL DEFAULT 0,
  origin_order INTEGER NOT NULL DEFAULT 0,
  variable TEXT,
  sync_time INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS search_books (
  book_url TEXT NOT NULL PRIMARY KEY,
  origin TEXT NOT NULL,
  origin_name TEXT,
  type INTEGER NOT NULL DEFAULT 0,
  name TEXT NOT NULL,
  author TEXT NOT NULL,
  kind TEXT,
  cover_url TEXT,
  intro TEXT,
  word_count TEXT,
  latest_chapter_title TEXT,
  toc_url TEXT,
  variable TEXT,
  origin_order INTEGER NOT NULL DEFAULT 0,
  respond_time INTEGER NOT NULL DEFAULT -1,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS chapters (
  book_id TEXT NOT NULL,
  book_url TEXT NOT NULL,
  url TEXT NOT NULL,
  title TEXT NOT NULL,
  is_volume INTEGER NOT NULL DEFAULT 0,
  base_url TEXT,
  chapter_index INTEGER NOT NULL,
  is_vip INTEGER NOT NULL DEFAULT 0,
  is_pay INTEGER NOT NULL DEFAULT 0,
  resource_url TEXT,
  tag TEXT,
  word_count TEXT,
  variable TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY(book_id, url),
  UNIQUE(book_id, chapter_index),
  FOREIGN KEY(book_id) REFERENCES books(book_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS replace_rules (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  rule_group TEXT,
  pattern TEXT NOT NULL,
  replacement TEXT NOT NULL DEFAULT '',
  scope TEXT,
  scope_title INTEGER NOT NULL DEFAULT 0,
  scope_content INTEGER NOT NULL DEFAULT 1,
  exclude_scope TEXT,
  is_enabled INTEGER NOT NULL DEFAULT 1,
  is_regex INTEGER NOT NULL DEFAULT 1,
  timeout_millisecond INTEGER NOT NULL DEFAULT 3000,
  sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS cookies (
  domain TEXT NOT NULL,
  name TEXT NOT NULL,
  value TEXT NOT NULL,
  path TEXT,
  expires_at TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY(domain, name)
);

CREATE TABLE IF NOT EXISTS source_variables (
  book_source_url TEXT NOT NULL,
  name TEXT NOT NULL,
  value TEXT,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY(book_source_url, name)
);

CREATE TABLE IF NOT EXISTS cache_entries (
  cache_key TEXT NOT NULL PRIMARY KEY,
  namespace TEXT NOT NULL,
  owner_type TEXT,
  owner_id TEXT,
  value BLOB,
  content_type TEXT,
  expires_at TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS debug_sessions (
  session_id TEXT NOT NULL PRIMARY KEY,
  source_id TEXT NOT NULL,
  mode TEXT NOT NULL,
  status TEXT NOT NULL,
  keyword TEXT,
  url TEXT,
  trace_id TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  completed_at TEXT
);

CREATE TABLE IF NOT EXISTS debug_events (
  event_id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  stage TEXT,
  level TEXT NOT NULL,
  message TEXT NOT NULL,
  elapsed_ms INTEGER NOT NULL DEFAULT 0,
  trace_id TEXT,
  data_json TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY(session_id) REFERENCES debug_sessions(session_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS check_tasks (
  task_id TEXT NOT NULL PRIMARY KEY,
  status TEXT NOT NULL,
  total INTEGER NOT NULL DEFAULT 0,
  finished INTEGER NOT NULL DEFAULT 0,
  trace_id TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  completed_at TEXT
);

CREATE TABLE IF NOT EXISTS check_task_items (
  task_id TEXT NOT NULL,
  source_id TEXT NOT NULL,
  book_source_url TEXT,
  book_source_name TEXT,
  status TEXT NOT NULL,
  failure_code TEXT,
  failure_class TEXT,
  message TEXT,
  respond_time INTEGER,
  trace_id TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY(task_id, source_id),
  FOREIGN KEY(task_id) REFERENCES check_tasks(task_id) ON DELETE CASCADE
);
`

func SchemaVersion(db *sql.DB) (int64, error) {
	var version int64
	if err := db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

func InitialSchemaVersion() int64 {
	return initialSchemaVersion
}

func ensureColumn(db *sql.DB, table string, column string, ddl string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var valueType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &valueType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(ddl)
	return err
}
