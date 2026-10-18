// Package bootstrap 装配 Reader 运行时（配置 → SQLite → 仓库 → 服务层），
// 供 HTTP serve 与 CLI 子命令共享，避免两套装配代码漂移。
package bootstrap

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
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
	"reader/internal/domain/source"
	"reader/internal/infra/config"
	"reader/internal/infra/fetcher"
	"reader/internal/infra/ratelimit"
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
	// 进程内的取消句柄与调试会话运行都在重启后丢失，遗留的未完成任务/会话无法再被取消或推进，
	// 启动时统一收敛为 failed，避免永久停在 running（Legado 由 service 生命周期管理同类状态）。
	if count, err := checkRepo.FailInterruptedTasks(context.Background(), time.Now().UTC()); err != nil {
		cleanup()
		return nil, nil, err
	} else if count > 0 {
		slog.Info("reconciled interrupted check tasks", "count", count)
	}
	if count, err := debugRepo.FailInterruptedSessions(context.Background(), time.Now().UTC()); err != nil {
		cleanup()
		return nil, nil, err
	} else if count > 0 {
		slog.Info("reconciled interrupted debug sessions", "count", count)
	}
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
	sourceVariables := repositories.NewSourceVariableRepository(db)
	httpFetcher := fetcher.NewHTTPFetcher(
		fetcher.WithTimeout(cfg.FetchTimeout),
		fetcher.WithMaxBodyBytes(cfg.FetchMaxBodyBytes),
		fetcher.WithMaxRedirects(cfg.FetchMaxRedirects),
		fetcher.WithAllowPrivateNetwork(cfg.AllowPrivateNetwork),
		fetcher.WithDefaultUserAgent(cfg.DefaultUserAgent),
		fetcher.WithCookieManager(cookieManager),
	)
	// 阶段由各调用点通过 script.WithStage 标注；书源级变量持久化到 source_variables，本地文件 API 限定在 <DataDir>/script-files。
	scriptRuntime := script.NewRuntime(script.Policy{
		Timeout:   cfg.ScriptTimeout,
		Fetcher:   httpFetcher,
		Cookie:    sessionCookies,
		Cache:     scriptCache,
		Variables: sourceVariables,
		FileRoot:  filepath.Join(cfg.DataDir, "script-files"),
	})
	importer := sourceapp.NewImporterWithFetcher(repo, httpFetcher)
	// 并发限流器按 READER_GLOBAL_CONCURRENCY / READER_SOURCE_CONCURRENCY 装配，搜索与发现共用同一实例；
	// 按书源 concurrentRate 的速率限速由 fetcher 承担（唯一限速点），这里只限制全局/单源并发。
	limiter := ratelimit.New(ratelimit.Options{Global: cfg.GlobalConcurrency, Source: cfg.SourceConcurrency})
	singleSearcher := searchapp.NewSingleSourceSearcher(httpFetcher, scriptRuntime).WithLimiter(limiter)
	multiSearcher := searchapp.NewMultiSourceSearcher(repo, singleSearcher).WithLimiter(limiter)
	discoveryService := discoveryapp.NewService(repo, httpFetcher, scriptRuntime).WithLimiter(limiter)
	// 校验专用：取消全局并发上限（并发由 READER_CHECK_CONCURRENCY 控制），保留单源上限保护目标站点。
	checkLimiter := ratelimit.New(ratelimit.Options{Source: cfg.SourceConcurrency})
	checkSearcher := searchapp.NewMultiSourceSearcher(repo, searchapp.NewSingleSourceSearcher(httpFetcher, scriptRuntime).WithLimiter(checkLimiter)).WithLimiter(checkLimiter)
	checkDiscovery := discoveryapp.NewService(repo, httpFetcher, scriptRuntime).WithLimiter(checkLimiter)
	// 书源导入/覆盖后失效两份探索分类缓存（搜索/发现共用与校验专用各一份），避免继续返回旧分类。
	importer.WithSavedCallback(func(u string) { discoveryService.InvalidateExploreKinds(u); checkDiscovery.InvalidateExploreKinds(u) })
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
	replaceManager := replaceapp.NewManager(replaceRepo).WithChangedCallback(contentProcessor.InvalidateRuleCache)
	debugPublisher := debugapp.NewPublisher(debugRepo)
	debugFlow := debugapp.NewKeywordFlow(multiSearcher, repo, bookService, contentApp, debugPublisher).WithDiscovery(discoveryService)
	debugRunner := debugapp.NewRunner(debugFlow, debugRepo)
	debugService := debugapp.NewService(debugRepo).WithRunner(debugRunner).WithPublisher(debugPublisher)
	checker := checkapp.NewChecker(checkSearcher, checkDiscovery, bookService, contentApp)
	checkEvents := checkapp.NewPublisher()
	checkRunner := checkworker.NewRunner(checkRepo, checker).WithDiagnostics(checkapp.NewDiagnosticsUpdater(repo)).WithEvents(checkEvents)
	checkManager := checkapp.NewManager(checkRepo, repo, checkRunner).WithEvents(checkEvents)
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
