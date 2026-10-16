// Package bootstrap 装配 Reader 运行时（配置 → SQLite → 仓库 → 服务层），
// 供 HTTP serve 与 CLI 子命令共享，避免两套装配代码漂移。
package bootstrap

import (
	"context"
	"database/sql"

	bookapp "reader/internal/app/book"
	checkapp "reader/internal/app/check"
	contentapp "reader/internal/app/content"
	debugapp "reader/internal/app/debug"
	discoveryapp "reader/internal/app/discovery"
	replaceapp "reader/internal/app/replace"
	searchapp "reader/internal/app/search"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/domain/source"
	"reader/internal/infra/config"
	"reader/internal/infra/fetcher"
	"reader/internal/infra/script"
	readersqlite "reader/internal/infra/sqlite"
	"reader/internal/infra/sqlite/repositories"
	checkworker "reader/internal/worker/check"
)

// Runtime 持有 CLI 与 serve 共用的完整服务装配。
type Runtime struct {
	Cfg            config.Config
	DB             *sql.DB
	Fetcher        *fetcher.HTTPFetcher
	ScriptRuntime  *script.Runtime
	SourceRepo     source.BookSourceRepository
	ChapterRepo    book.ChapterRepository
	Sources        *sourceapp.Manager
	Importer       *sourceapp.Importer
	Searcher       *searchapp.MultiSourceSearcher
	Discovery      *discoveryapp.Service
	Books          *bookapp.Service
	Content        *contentapp.AppService
	ImageProxy     *contentapp.ImageProxy
	Replace        *replaceapp.Manager
	Debug          *debugapp.Service
	DebugPublisher *debugapp.Publisher
	Check          *checkapp.Manager
}

// Open 按 cmd/server 原装配顺序构建 Runtime；cleanup 关闭数据库。
func Open(cfg config.Config) (*Runtime, func(), error) {
	db, err := readersqlite.Open(cfg.SQLiteDSN)
	if err != nil {
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	cleanup := func() {
		_ = db.Close()
	}
	if err := readersqlite.Migrate(db); err != nil {
		cleanup()
		return nil, nil, err
	}
	repo := repositories.NewBookSourceRepository(db)
	bookRepo := repositories.NewBookRepository(db)
	chapterRepo := repositories.NewChapterRepository(db)
	replaceRepo := repositories.NewReplaceRuleRepository(db)
	debugRepo := repositories.NewDebugRepository(db)
	checkRepo := repositories.NewCheckRepository(db)
	imageCacheRepo := repositories.NewImageCacheRepository(db)
	// 启动时清理过期图片缓存，防止 cache_entries 无限增长。
	if _, err := imageCacheRepo.DeleteExpired(context.Background()); err != nil {
		cleanup()
		return nil, nil, err
	}
	cookieRepo := repositories.NewCookieRepository(db)
	manager := sourceapp.NewManager(repo)
	sessionCookies := fetcher.NewSessionCookieStore()
	cookieManager := fetcher.NewCookieManager(cookieRepo, sessionCookies)
	scriptCache := script.NewNamespacedCache()
	httpFetcher := fetcher.NewHTTPFetcher(
		fetcher.WithTimeout(cfg.FetchTimeout),
		fetcher.WithMaxBodyBytes(cfg.FetchMaxBodyBytes),
		fetcher.WithMaxRedirects(cfg.FetchMaxRedirects),
		fetcher.WithAllowPrivateNetwork(cfg.AllowPrivateNetwork),
		fetcher.WithDefaultUserAgent(cfg.DefaultUserAgent),
		fetcher.WithCookieManager(cookieManager),
	)
	scriptRuntime := script.NewRuntime(script.Policy{Timeout: cfg.ScriptTimeout, Stage: "search", Fetcher: httpFetcher, Cookie: sessionCookies, Cache: scriptCache})
	importer := sourceapp.NewImporterWithFetcher(repo, httpFetcher)
	singleSearcher := searchapp.NewSingleSourceSearcher(httpFetcher, scriptRuntime)
	multiSearcher := searchapp.NewMultiSourceSearcher(repo, singleSearcher)
	discoveryService := discoveryapp.NewService(repo, httpFetcher, scriptRuntime)
	infoService := bookapp.NewInfoService(bookRepo, httpFetcher, scriptRuntime)
	tocService := bookapp.NewTocService(chapterRepo, httpFetcher, scriptRuntime)
	bookService := bookapp.NewService(bookRepo, chapterRepo, repo, infoService, tocService)
	contentProcessor := replaceapp.NewProcessor(replaceRepo).WithOptions(replaceapp.ProcessorOptions{
		ReSegmentEnabled:     cfg.ContentReSegment,
		ChineseConverterType: cfg.ContentChineseConverterType,
	})
	contentService := contentapp.NewService(chapterRepo, httpFetcher, scriptRuntime).WithProcessor(contentProcessor)
	contentApp := contentapp.NewAppService(bookRepo, chapterRepo, repo, contentService)
	imageProxy := contentapp.NewImageProxy(httpFetcher, repo, bookRepo, scriptRuntime).WithCache(imageCacheRepo)
	replaceManager := replaceapp.NewManager(replaceRepo)
	debugPublisher := debugapp.NewPublisher(debugRepo)
	debugFlow := debugapp.NewKeywordFlow(multiSearcher, bookService, contentApp, debugPublisher).WithDiscovery(discoveryService)
	debugRunner := debugapp.NewRunner(debugFlow)
	debugService := debugapp.NewService(debugRepo).WithRunner(debugRunner)
	checker := checkapp.NewChecker(multiSearcher, discoveryService, bookService, contentApp)
	checkRunner := checkworker.NewRunner(checkRepo, checker).WithDiagnostics(checkapp.NewDiagnosticsUpdater(repo))
	checkManager := checkapp.NewManager(checkRepo, repo, checkRunner)
	return &Runtime{
		Cfg:            cfg,
		DB:             db,
		Fetcher:        httpFetcher,
		ScriptRuntime:  scriptRuntime,
		SourceRepo:     repo,
		ChapterRepo:    chapterRepo,
		Sources:        manager,
		Importer:       importer,
		Searcher:       multiSearcher,
		Discovery:      discoveryService,
		Books:          bookService,
		Content:        contentApp,
		ImageProxy:     imageProxy,
		Replace:        replaceManager,
		Debug:          debugService,
		DebugPublisher: debugPublisher,
		Check:          checkManager,
	}, cleanup, nil
}
