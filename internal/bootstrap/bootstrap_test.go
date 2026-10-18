package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	checkapp "reader/internal/app/check"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
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
	if _, err := rt.Check.Start(ctx, checkTask, []string{imported.Items[0].SourceID}, checkapp.Options{
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
		t.Fatalf("task status = %q, items = %+v", got.Status, got.Items)
	}
	if len(got.Items) != 1 || got.Items[0].Status != task.CheckItemPassed {
		t.Fatalf("items = %+v", got.Items)
	}
	// 四个阶段都必须真实执行（passed），否则“零写入”可能只是链路被跳过。
	for _, want := range []task.CheckStageName{task.CheckStageSearch, task.CheckStageInfo, task.CheckStageToc, task.CheckStageContent} {
		status := task.CheckTaskItemStatus("")
		for _, stage := range got.Items[0].Stages {
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
