package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	checkapp "reader/internal/app/check"
	searchapp "reader/internal/app/search"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	rulepkg "reader/internal/domain/rule"
	"reader/internal/domain/source"
	"reader/internal/domain/task"
	"reader/internal/infra/config"
)

// newTestConfig 构造指向临时目录的最小可用配置。
func newTestConfig(t *testing.T) config.Config {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	cfg, err := config.Load(config.FromMap(map[string]string{
		"READER_DATA_DIR":   dir,
		"READER_SQLITE_DSN": filepath.Join(dir, "reader.db"),
		"READER_ENV":        config.EnvTest,
	}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// TestOpenCreatesMigratedDatabaseAndClosesOnCleanup 验证装配入口的真实副作用：
// 建库并把迁移跑到最新版本、收紧数据目录与库文件权限、cleanup 后连接不可用。
func TestOpenCreatesMigratedDatabaseAndClosesOnCleanup(t *testing.T) {
	cfg := newTestConfig(t)
	rt, cleanup, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var version int64
	if err := rt.DB.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("查询 schema_migrations: %v", err)
	}
	if version < 4 {
		t.Fatalf("schema version = %d, 迁移未跑到最新", version)
	}
	var dropped int
	if err := rt.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='search_books'").Scan(&dropped); err != nil {
		t.Fatalf("查询 sqlite_master: %v", err)
	}
	if dropped != 0 {
		t.Fatal("search_books 表应已被迁移删除")
	}

	info, err := os.Stat(cfg.SQLiteDSN)
	if err != nil {
		t.Fatalf("stat db: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("db perm = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(cfg.SQLiteDSN))
	if err != nil {
		t.Fatalf("stat data dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("data dir perm = %o, want 700", perm)
	}

	cleanup()
	if err := rt.DB.Ping(); err == nil {
		t.Fatal("cleanup 之后数据库仍可用")
	}
}

// TestOpenRuntimePersistsSources 验证装配出的运行时确实把数据落到同一份 SQLite：
// 仓库写入、导入、再读回都命中同一个库文件。
func TestOpenRuntimePersistsSources(t *testing.T) {
	cfg := newTestConfig(t)
	rt, cleanup, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer cleanup()

	ctx := context.Background()
	if err := rt.SourceRepo.Save(ctx, &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://example.test",
		BookSourceName: "示例源",
		Enabled:        true,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := rt.Importer.Import(ctx, sourceapp.ImportInput{
		Payload: []byte(`[{"bookSourceUrl":"https://other.test","bookSourceName":"另一个源"}]`),
	}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	got, err := rt.SourceRepo.FindByURL(ctx, "https://other.test")
	if err != nil {
		t.Fatalf("FindByURL: %v", err)
	}
	if got.BookSourceName != "另一个源" {
		t.Fatalf("bookSourceName = %q", got.BookSourceName)
	}
}

// TestOpenFailsWhenDataPathIsDirectory 验证不可写路径不会静默成功。
func TestOpenFailsWhenDataPathIsDirectory(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(config.FromMap(map[string]string{
		"READER_DATA_DIR":   dir,
		"READER_SQLITE_DSN": dir,
		"READER_ENV":        config.EnvTest,
	}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, _, err := Open(cfg); err == nil {
		t.Fatal("以目录作为 DSN 时 Open 应失败")
	}
}

// TestCheckDoesNotTouchBookshelf 是「校验不入书架」的端到端回归：
// 一轮搜索→详情→目录→正文校验跑通后，books/chapters/cache_entries 必须全部为空
// （对齐 Legado CheckSourceService.checkBook 的 needSave=false）。
func TestCheckDoesNotTouchBookshelf(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sources.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		src := map[string]any{
			"bookSourceUrl":  "http://" + r.Host,
			"bookSourceName": "Check Mock Source",
			"bookSourceType": 0,
			"enabled":        true,
			"searchUrl":      "http://" + r.Host + "/search?key={{key}}&page={{page}}",
			"ruleSearch":     map[string]any{"bookList": ".item", "name": ".name@text", "author": ".author@text", "bookUrl": ".name@href", "checkKeyWord": "斗破", "tocUrl": ".toc@href"},
			"ruleBookInfo":   map[string]any{"name": "h1@text", "author": ".author@text", "tocUrl": ".toc@href", "canReName": "1"},
			"ruleToc":        map[string]any{"chapterList": ".chapter", "chapterName": "a@text", "chapterUrl": "a@href", "isVolume": ".isvol@text"},
			"ruleContent":    map[string]any{"content": ".content@text"},
		}
		raw, _ := json.Marshal(src)
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="item"><a class="name" href="/book/1">斗破苍穹</a><span class="author">天蚕土豆</span></div>`))
	})
	mux.HandleFunc("/book/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<h1>斗破苍穹</h1><span class="author">天蚕土豆</span><a class="toc" href="/toc/1">目录</a>`))
	})
	mux.HandleFunc("/toc/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<ul><li class="chapter"><a href="/content/1">第一章</a><span class="isvol">no</span></li><li class="chapter"><a href="/content/2">第二章</a><span class="isvol">no</span></li></ul>`))
	})
	mux.HandleFunc("/content/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="content">正文内容</div>`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	dir := filepath.Join(t.TempDir(), "data")
	cfg, err := config.Load(config.FromMap(map[string]string{
		"READER_DATA_DIR":              dir,
		"READER_SQLITE_DSN":            filepath.Join(dir, "reader.db"),
		"READER_ENV":                   config.EnvTest,
		"READER_ALLOW_PRIVATE_NETWORK": "true",
	}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	rt, cleanup, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(cleanup)
	ctx := t.Context()

	imported, err := rt.Importer.Import(ctx, sourceapp.ImportInput{RemoteURL: server.URL + "/sources.json"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(imported.Items) != 1 || imported.Items[0].SourceID == "" {
		t.Fatalf("import result = %+v", imported)
	}

	checkTask := &task.CheckTask{TaskID: "check-transient"}
	if err := rt.Check.Start(ctx, checkTask, []string{imported.Items[0].SourceID}, checkapp.Options{
		Keyword:      "斗破苍穹",
		CheckSearch:  true,
		CheckInfo:    true,
		CheckToc:     true,
		CheckContent: true,
	}, 0); err != nil {
		t.Fatalf("Check.Start: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	var got *task.CheckTask
	for {
		got, err = rt.Check.Get(ctx, "check-transient")
		if err != nil {
			t.Fatalf("Check.Get: %v", err)
		}
		if task.CheckTaskTerminal(got.Status) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("check task stuck in %q", got.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if got.Status != task.CheckTaskSucceeded {
		t.Fatalf("task status = %q", got.Status)
	}
	// FindTask 不再内联条目（S03-06）：条目走 ListItemsPage 按页读取。
	items, _, err := rt.Check.ListItems(ctx, "check-transient", nil, 1, 100)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 || items[0].Status != task.CheckItemPassed {
		t.Fatalf("items = %+v", items)
	}
	// 四个阶段都必须真实执行（passed），否则“零写入”可能只是链路被跳过。
	for _, want := range []task.CheckStageName{task.CheckStageSearch, task.CheckStageInfo, task.CheckStageToc, task.CheckStageContent} {
		status := task.CheckTaskItemStatus("")
		for _, stage := range items[0].Stages {
			if stage.Stage == want {
				status = stage.Status
			}
		}
		if status != task.CheckItemPassed {
			t.Fatalf("stage %q = %q, stages = %+v", want, status, got.Items[0].Stages)
		}
	}

	books, total, err := rt.Books.List(ctx, book.BookFilter{})
	if err != nil {
		t.Fatalf("Books.List: %v", err)
	}
	if total != 0 || len(books) != 0 {
		t.Fatalf("bookshelf polluted: total=%d books=%+v", total, books)
	}
	for _, table := range []string{"chapters", "cache_entries"} {
		var count int
		if err := rt.DB.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s has %d rows, want 0", table, count)
		}
	}
}

// S11-03：WAL 读写分离连接池——读连接并发放开（>1），写并发由 WAL + busy_timeout 兜底。
// 并发读与并发写同时压测，任何一方都不应把另一方完全阻塞到超时。
func TestOpenEnablesConcurrentReadsAndWrites(t *testing.T) {
	cfg := newTestConfig(t)
	rt, cleanup, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer cleanup()

	ctx := context.Background()
	if err := rt.SourceRepo.Save(ctx, &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  "https://example.test",
		BookSourceName: "示例源",
		Enabled:        true,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	done := make(chan error, 2)
	go func() { // 并发读
		for range 50 {
			qCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := rt.DB.QueryContext(qCtx, "SELECT count(*) FROM book_sources")
			cancel()
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	go func() { // 并发写
		for i := range 50 {
			wCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := rt.SourceRepo.Save(wCtx, &source.BookSource{
				SourceID:       fmt.Sprintf("w%d", i),
				BookSourceURL:  fmt.Sprintf("https://w%d.example.test", i),
				BookSourceName: "并发写",
				Enabled:        true,
			})
			cancel()
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	// 带截止的等待：读/写任一方若被完全阻塞（WAL 并行能力失效）则超时失败而非挂起。
	deadline := time.Now().Add(10 * time.Second)
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("并发读/写失败: %v", err)
			}
		case <-time.After(time.Until(deadline)):
			t.Fatalf("并发读/写在 10s 内未完成，WAL 并行能力未生效")
		}
	}
	var count int
	if err := rt.DB.QueryRow("SELECT count(*) FROM book_sources").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 51 {
		t.Fatalf("book_sources = %d rows, want 51", count)
	}
}

// S16-07：StartupCleanup=false 时 3 条全表扫描清理语句不执行——遗留未完成任务保持原状、
// 过期缓存行不被删除；StartupCleanup=true（Open 默认）时收敛/删除照常执行。
func TestStartupCleanupGating(t *testing.T) {
	build := func(t *testing.T) (*Runtime, func()) {
		cfg := newTestConfig(t)
		rt, cleanup, err := Open(cfg)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		return rt, cleanup
	}
	seed := func(t *testing.T, rt *Runtime) {
		t.Helper()
		ctx := context.Background()
		now := time.Now().UTC().Add(-time.Hour)
		if _, err := rt.DB.ExecContext(ctx,
			"INSERT INTO check_tasks (task_id, status, created_at) VALUES ('stale', 'running', ?)", now); err != nil {
			t.Fatalf("seed check_tasks: %v", err)
		}
		if _, err := rt.DB.ExecContext(ctx,
			"INSERT INTO cache_entries (cache_key, namespace, value, created_at) VALUES ('k', 'image', ?, ?)", []byte("x"), now); err != nil {
			t.Fatalf("seed cache_entries: %v", err)
		}
	}

	// 关闭清理：在同库重开（StartupCleanup=false），遗留 running 任务与过期缓存行保持原状。
	rt, cleanup := build(t)
	seed(t, rt)
	cleanup()

	reopened, cleanup3, err := OpenWithOptions(cfgForSameDB(t, rt.Cfg.SQLiteDSN), Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer cleanup3()
	var status string
	if err := reopened.DB.QueryRow("SELECT status FROM check_tasks WHERE task_id='stale'").Scan(&status); err != nil {
		t.Fatalf("query check_tasks: %v", err)
	}
	if status != "running" {
		t.Fatalf("status = %q, want running（清理应被跳过）", status)
	}
	var cacheCount int
	if err := reopened.DB.QueryRow("SELECT count(*) FROM cache_entries WHERE cache_key='k'").Scan(&cacheCount); err != nil {
		t.Fatalf("query cache_entries: %v", err)
	}
	if cacheCount != 1 {
		t.Fatalf("cache_entries = %d rows, want 1（清理应被跳过）", cacheCount)
	}
	cleanup()

	// 开启清理（Open 默认）：running 任务收敛为 failed、过期缓存被删除。
	rt4, cleanup4, err := OpenWithOptions(cfgForSameDB(t, rt.Cfg.SQLiteDSN), Options{StartupCleanup: true})
	if err != nil {
		t.Fatalf("OpenWithOptions(StartupCleanup=true): %v", err)
	}
	defer cleanup4()
	if err := rt4.DB.QueryRow("SELECT status FROM check_tasks WHERE task_id='stale'").Scan(&status); err != nil {
		t.Fatalf("query check_tasks: %v", err)
	}
	if status != "failed" {
		t.Fatalf("status = %q, want failed（清理应执行）", status)
	}
	if err := rt4.DB.QueryRow("SELECT count(*) FROM cache_entries WHERE cache_key='k'").Scan(&cacheCount); err != nil {
		t.Fatalf("query cache_entries: %v", err)
	}
	if cacheCount != 0 {
		t.Fatalf("cache_entries = %d rows, want 0（清理应执行）", cacheCount)
	}
}

// cfgForSameDB 构造指向同一 SQLite 文件的最小配置。
func cfgForSameDB(t *testing.T, dsn string) config.Config {
	t.Helper()
	cfg, err := config.Load(config.FromMap(map[string]string{
		"READER_DATA_DIR":   filepath.Dir(dsn),
		"READER_SQLITE_DSN": dsn,
		"READER_ENV":        config.EnvTest,
	}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// 契约 3：Open 装配时把 cfg.SearchSourceTimeout 接到两份 MultiSourceSearcher。
// 多源搜索在单源 fetcher 挂起时应在每源超时内收到 search_timeout warning，而非等到整体截止。
func TestSearcherUsesConfiguredSourceTimeout(t *testing.T) {
	block := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		<-block // 挂起直到测试结束，触发每源超时而非 fetcher 超时
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(block) })

	dir := filepath.Join(t.TempDir(), "data")
	cfg, err := config.Load(config.FromMap(map[string]string{
		"READER_DATA_DIR":              dir,
		"READER_SQLITE_DSN":            filepath.Join(dir, "reader.db"),
		"READER_ENV":                   config.EnvTest,
		"READER_SEARCH_SOURCE_TIMEOUT": "300ms",
		"READER_FETCH_TIMEOUT":         "30s",
		"READER_ALLOW_PRIVATE_NETWORK": "true",
	}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	rt, cleanup, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer cleanup()

	ctx := context.Background()
	if err := rt.SourceRepo.Save(ctx, &source.BookSource{
		SourceID:       "s1",
		BookSourceURL:  server.URL,
		BookSourceName: "挂起源",
		Enabled:        true,
		SearchURL:      server.URL + "/search?key={{key}}",
		RuleSearch:     rulepkg.Flexible[rulepkg.SearchRule]{Value: &rulepkg.SearchRule{BookList: ".item"}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	start := time.Now()
	result, err := rt.Searcher.Search(ctx, searchapp.MultiSearchInput{Keyword: "x"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("Search elapsed = %s, 每源超时未生效（应为 ~300ms）", elapsed)
	}
	found := false
	for _, w := range result.Warnings {
		if w.Code == "search_timeout" || strings.Contains(w.Message, "timeout") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want search_timeout warning", result.Warnings)
	}
}
