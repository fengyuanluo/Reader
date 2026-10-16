package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// migration 是一个版本化迁移步骤；apply 与版本记录在同一事务内完成。
type migration struct {
	version int64
	name    string
	apply   func(ctx context.Context, tx *sql.Tx) error
}

// migrations 按版本升序排列。已发布的版本禁止修改，结构变更只能追加新版本。
var migrations = []migration{
	{version: 1, name: "initial schema", apply: execSQL(initialSchema)},
	{version: 2, name: "check_task_items.failure_class", apply: addCheckItemFailureClass},
	{version: 3, name: "drop unused search_books", apply: execSQL(`DROP TABLE IF EXISTS search_books`)},
	{version: 4, name: "debug_events session index", apply: execSQL(`CREATE INDEX IF NOT EXISTS idx_debug_events_session ON debug_events(session_id, event_id)`)},
	// 版本 5：正文缓存改为保存净化前正文，旧库里保存的是已应用用户替换规则的内容，清掉等待重新抓取。
	{version: 5, name: "drop preprocessed content cache", apply: execSQL(`DELETE FROM cache_entries WHERE namespace = 'content'`)},
}

// Migrate 把数据库升级到最新版本。连接级 PRAGMA（外键、busy_timeout、WAL）由 Open 通过 DSN 设置。
func Migrate(db *sql.DB) error {
	return applyMigrations(context.Background(), db, migrations)
}

func applyMigrations(ctx context.Context, db *sql.DB, steps []migration) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	var current int64
	if err := db.QueryRowContext(ctx, `SELECT coalesce(max(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for _, step := range steps {
		if step.version <= current {
			continue
		}
		if err := applyMigration(ctx, db, step); err != nil {
			return fmt.Errorf("migration %d (%s): %w", step.version, step.name, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, step migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := step.apply(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES (?)`, step.version); err != nil {
		return err
	}
	return tx.Commit()
}

func execSQL(statements string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, statements)
		return err
	}
}

// addCheckItemFailureClass 兼容旧库：版本机制引入前 failure_class 已可能被探测式补列添加过。
func addCheckItemFailureClass(ctx context.Context, tx *sql.Tx) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('check_task_items') WHERE name = 'failure_class'`).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE check_task_items ADD COLUMN failure_class TEXT`)
	return err
}

// initialSchema 是版本 1 的建表语句，已发布后不再修改；新结构变更只追加迁移版本。
const initialSchema = `
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
