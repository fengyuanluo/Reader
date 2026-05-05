package e2e_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bookapp "reader/internal/app/book"
	checkapp "reader/internal/app/check"
	contentapp "reader/internal/app/content"
	debugapp "reader/internal/app/debug"
	discoveryapp "reader/internal/app/discovery"
	replaceapp "reader/internal/app/replace"
	searchapp "reader/internal/app/search"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/domain/replace"
	domainsource "reader/internal/domain/source"
	"reader/internal/domain/task"
	"reader/internal/infra/fetcher"
	"reader/internal/infra/script"
	readersqlite "reader/internal/infra/sqlite"
	"reader/internal/infra/sqlite/repositories"
	checkworker "reader/internal/worker/check"

	_ "modernc.org/sqlite"
)

func TestMockOnlineSourceFullFlow(t *testing.T) {
	ctx := context.Background()
	mock := newMockSourceServer(t)
	env := newE2EEnv(t)

	sourceJSON := mock.sourceJSON()
	imported, err := env.importer.Import(ctx, sourceapp.ImportInput{Payload: []byte(sourceJSON)})
	if err != nil {
		t.Fatalf("import mock source: %v", err)
	}
	if imported.Added != 1 {
		t.Fatalf("import result = %#v", imported)
	}

	searchResult, err := env.search.Search(ctx, searchapp.MultiSearchInput{Keyword: "斗破", Page: 1, EnabledOnly: true, Concurrency: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(searchResult.Items) != 1 || searchResult.Items[0].Name != "斗破苍穹" {
		t.Fatalf("search result = %#v", searchResult)
	}

	saved, err := env.books.Save(ctx, ptr(searchResult.Items[0].ToBook()))
	if err != nil {
		t.Fatalf("save book: %v", err)
	}
	bookID := book.IDFromURL(saved.BookURL)
	if _, err := env.books.RefreshInfo(ctx, bookID); err != nil {
		t.Fatalf("refresh info: %v", err)
	}
	chapters, _, err := env.books.RefreshToc(ctx, bookID)
	if err != nil {
		t.Fatalf("refresh toc: %v", err)
	}
	if len(chapters) != 1 || chapters[0].Title != "第一章" {
		t.Fatalf("chapters = %#v", chapters)
	}

	if _, err := env.replace.Create(ctx, &replace.ReplaceRule{
		Name:               "ad",
		Pattern:            "广告",
		Replacement:        "",
		ScopeContent:       true,
		IsEnabled:          true,
		TimeoutMillisecond: 3000,
	}); err != nil {
		t.Fatalf("create replace rule: %v", err)
	}
	content, _, err := env.content.GetChapterContent(ctx, bookID, 0)
	if err != nil {
		t.Fatalf("content: %v", err)
	}
	if strings.Contains(content.Content, "广告") || !strings.Contains(content.Content, "真正正文") {
		t.Fatalf("processed content = %q", content.Content)
	}
	firstContentRequests := mock.contentRequests
	if _, _, err := env.content.GetChapterContent(ctx, bookID, 0); err != nil {
		t.Fatalf("cached content: %v", err)
	}
	if mock.contentRequests != firstContentRequests {
		t.Fatalf("content cache miss: before=%d after=%d", firstContentRequests, mock.contentRequests)
	}

	progressed, err := env.books.SaveProgress(ctx, bookID, book.BookProgress{DurChapterIndex: 0, DurChapterPos: 12, DurChapterTitle: "第一章", DurChapterTime: 123})
	if err != nil {
		t.Fatalf("save progress: %v", err)
	}
	if progressed.DurChapterPos != 12 || progressed.DurChapterTitle != "第一章" {
		t.Fatalf("progressed = %#v", progressed)
	}

	session := &task.DebugSession{SessionID: "debug-e2e", SourceID: imported.Items[0].SourceID, Mode: task.DebugModeKeyword, Keyword: "斗破", Status: task.DebugSessionPending, CreatedAt: time.Now().UTC()}
	if err := env.debugRepo.CreateSession(ctx, session); err != nil {
		t.Fatalf("create debug session: %v", err)
	}
	if err := env.debugFlow.RunKeyword(ctx, session, "斗破"); err != nil {
		t.Fatalf("debug flow: %v", err)
	}
	events, err := env.debugRepo.ListEvents(ctx, session.SessionID, 0)
	if err != nil {
		t.Fatalf("debug events: %v", err)
	}
	if !debugContainsStages(events, "search", "info", "toc", "content") {
		t.Fatalf("debug events missing stages: %#v", events)
	}

	checkTask := &task.CheckTask{TaskID: "check-e2e", Status: task.CheckTaskPending, CreatedAt: time.Now().UTC()}
	src, err := env.sources.FindByID(ctx, imported.Items[0].SourceID)
	if err != nil {
		t.Fatalf("find imported source: %v", err)
	}
	if err := env.checkRunner.Start(ctx, checkTask, []*domainsource.BookSource{src}, checkapp.Options{Keyword: "斗破", CheckSearch: true, CheckInfo: true, CheckToc: true, CheckContent: true}, 1); err != nil {
		t.Fatalf("start check: %v", err)
	}
	waitForCheckTask(t, env.checkRepo, checkTask.TaskID)
	checked, err := env.checkRepo.FindTask(ctx, checkTask.TaskID)
	if err != nil {
		t.Fatalf("find check task: %v", err)
	}
	if checked.Status != task.CheckTaskSucceeded || checked.Finished != 1 {
		t.Fatalf("checked = %#v", checked)
	}
}

type e2eEnv struct {
	db          *sql.DB
	sources     *repositories.BookSourceRepository
	importer    *sourceapp.Importer
	search      *searchapp.MultiSourceSearcher
	books       *bookapp.Service
	content     *contentapp.AppService
	replace     *replaceapp.Manager
	debugRepo   *repositories.DebugRepository
	debugFlow   *debugapp.KeywordFlow
	checkRepo   *repositories.CheckRepository
	checkRunner *checkworker.Runner
}

func newE2EEnv(t *testing.T) *e2eEnv {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := readersqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sourceRepo := repositories.NewBookSourceRepository(db)
	bookRepo := repositories.NewBookRepository(db)
	chapterRepo := repositories.NewChapterRepository(db)
	replaceRepo := repositories.NewReplaceRuleRepository(db)
	debugRepo := repositories.NewDebugRepository(db)
	checkRepo := repositories.NewCheckRepository(db)
	cookieRepo := repositories.NewCookieRepository(db)
	sessionCookies := fetcher.NewSessionCookieStore()
	cookieManager := fetcher.NewCookieManager(cookieRepo, sessionCookies)
	scriptCache := script.NewNamespacedCache()
	httpFetcher := fetcher.NewHTTPFetcher(
		fetcher.WithAllowPrivateNetwork(true),
		fetcher.WithDefaultUserAgent("Reader-E2E"),
		fetcher.WithCookieManager(cookieManager),
	)
	scriptRuntime := script.NewRuntime(script.Policy{Timeout: 10 * time.Second, Stage: "e2e", Fetcher: httpFetcher, Cookie: sessionCookies, Cache: scriptCache})
	importer := sourceapp.NewImporterWithFetcher(sourceRepo, httpFetcher)
	singleSearch := searchapp.NewSingleSourceSearcher(httpFetcher, scriptRuntime)
	searcher := searchapp.NewMultiSourceSearcher(sourceRepo, singleSearch)
	discovery := discoveryapp.NewService(sourceRepo, httpFetcher, scriptRuntime)
	info := bookapp.NewInfoService(bookRepo, httpFetcher, scriptRuntime)
	toc := bookapp.NewTocService(chapterRepo, httpFetcher, scriptRuntime)
	books := bookapp.NewService(bookRepo, chapterRepo, sourceRepo, info, toc)
	contentService := contentapp.NewService(chapterRepo, httpFetcher, scriptRuntime).WithProcessor(replaceapp.NewProcessor(replaceRepo))
	content := contentapp.NewAppService(bookRepo, chapterRepo, sourceRepo, contentService)
	publisher := debugapp.NewPublisher(debugRepo)
	debugFlow := debugapp.NewKeywordFlow(searcher, books, content, publisher).WithDiscovery(discovery)
	checker := checkapp.NewChecker(searcher, discovery, books, content)
	checkRunner := checkworker.NewRunner(checkRepo, checker).WithDiagnostics(checkapp.NewDiagnosticsUpdater(sourceRepo))
	return &e2eEnv{db: db, sources: sourceRepo, importer: importer, search: searcher, books: books, content: content, replace: replaceapp.NewManager(replaceRepo), debugRepo: debugRepo, debugFlow: debugFlow, checkRepo: checkRepo, checkRunner: checkRunner}
}

type mockSourceServer struct {
	*httptest.Server
	contentRequests int
}

func newMockSourceServer(t *testing.T) *mockSourceServer {
	t.Helper()
	mock := &mockSourceServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="item"><a class="name" href="/book/1">斗破苍穹</a><span class="author">天蚕土豆</span><span class="kind">玄幻</span><span class="word">500万字</span><span class="latest">第一章</span><img class="cover" src="/cover.png"/></div>`))
	})
	mux.HandleFunc("/book/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<h1>斗破苍穹</h1><span class="author">天蚕土豆</span><div class="kind">玄幻</div><span class="word">500万字</span><span class="latest">第一章</span><p class="intro">少年成长</p><img class="cover" src="/cover.png"/><a class="toc" href="/toc/1">目录</a>`))
	})
	mux.HandleFunc("/toc/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<ul><li class="chapter"><a href="/content/1">第一章</a></li></ul>`))
	})
	mux.HandleFunc("/content/1", func(w http.ResponseWriter, r *http.Request) {
		mock.contentRequests++
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="content">广告
真正正文</div>`))
	})
	mock.Server = httptest.NewServer(mux)
	t.Cleanup(mock.Close)
	return mock
}

func (m *mockSourceServer) sourceJSON() string {
	source := map[string]any{
		"bookSourceUrl":    m.URL,
		"bookSourceName":   "Mock Source",
		"bookSourceType":   0,
		"enabled":          true,
		"enabledExplore":   true,
		"enabledCookieJar": true,
		"searchUrl":        m.URL + "/search?key={{key}}&page={{page}}",
		"ruleSearch": map[string]any{
			"bookList":          ".item",
			"name":              ".name@text",
			"author":            ".author@text",
			"kind":              ".kind@text",
			"wordCount":         ".word@text",
			"lastChapter":       ".latest@text",
			"coverUrl":          ".cover@src",
			"bookUrl":           ".name@href",
			"checkKeyWord":      "斗破",
			"intro":             ".intro@text",
			"tocUrl":            ".toc@href",
			"latestChapterTime": "",
		},
		"ruleBookInfo": map[string]any{
			"name":            "h1@text",
			"author":          ".author@text",
			"kind":            ".kind@text",
			"wordCount":       ".word@text",
			"lastChapter":     ".latest@text",
			"intro":           ".intro@text",
			"coverUrl":        ".cover@src",
			"tocUrl":          ".toc@href",
			"canReName":       "1",
			"downloadUrls":    "",
			"init":            "",
			"lastChapterTime": "",
		},
		"ruleToc": map[string]any{
			"chapterList": ".chapter",
			"chapterName": "a@text",
			"chapterUrl":  "a@href",
		},
		"ruleContent": map[string]any{
			"content": ".content@text",
		},
	}
	raw, _ := json.Marshal(source)
	return string(raw)
}

func ptr[T any](value T) *T {
	return &value
}

func debugContainsStages(events []*task.DebugEvent, stages ...string) bool {
	seen := map[string]bool{}
	for _, event := range events {
		seen[event.Stage] = true
	}
	for _, stage := range stages {
		if !seen[stage] {
			return false
		}
	}
	return true
}

func waitForCheckTask(t *testing.T, repo *repositories.CheckRepository, taskID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		checkTask, err := repo.FindTask(context.Background(), taskID)
		if err == nil && (checkTask.Status == task.CheckTaskSucceeded || checkTask.Status == task.CheckTaskFailed || checkTask.Status == task.CheckTaskCancelled) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	checkTask, _ := repo.FindTask(context.Background(), taskID)
	t.Fatalf("check task did not finish: %#v", checkTask)
}
