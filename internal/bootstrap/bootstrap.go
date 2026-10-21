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

// Options 控制 Runtime 装配的可选行为；零值字段沿用默认（与 Open 一致）。
type Options struct {
	// StartupCleanup 控制 3 条启动清理语句（中断任务/会话收敛、过期缓存清理）：
	// CLI 短命令跳过可避免每条子命令的全表扫描；serve 启动必须为 true。
	StartupCleanup bool
}

// maxReadConns 是 SQLite 读连接并发上限：WAL 下多读 + 单写并行，
// 4 条连接足以覆盖 HTTP 读与校验进度写，单写事务由 BeginTx 串行。
const maxReadConns = 4

// Open 按 cmd/server 原装配顺序构建 Runtime；cleanup 关闭数据库。
// 等价于 OpenWithOptions(cfg, Options{StartupCleanup: true})。
func Open(cfg config.Config) (*Runtime, func(), error) {
	return OpenWithOptions(cfg, Options{StartupCleanup: true})
}

// OpenWithOptions 构建 Runtime；opts.StartCleanup 为 false 时跳过启动清理语句。
func OpenWithOptions(cfg config.Config, opts Options) (*Runtime, func(), error) {
	db, err := readersqlite.Open(cfg.SQLiteDSN)
	if err != nil {
		return nil, nil, err
	}
	// WAL 模式允许多读 + 单写并行：读连接并发放开（写并发由 WAL + busy_timeout 兜底，
	// 单写事务语义已由 BeginTx 保证），避免一次慢查询阻塞全部读写。
	db.SetMaxOpenConns(maxReadConns)
	db.SetMaxIdleConns(maxReadConns)
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
	// 启动清理（3 条全表扫描语句）只在 serve 启动执行：CLI 短命令（如 reader toc/source list）
	// 直连数据库且不持有进程内取消句柄，跳过可避免每条子命令付出全表扫描延迟。
	if opts.StartupCleanup {
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
		// 启动时清理过期图片缓存，防止 cache_entries 无限增长。
		if _, err := imageCacheRepo.DeleteExpired(context.Background()); err != nil {
			cleanup()
			return nil, nil, err
		}
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
	// 书源删除/改名时清理限流器里旧 bookSourceUrl 的单源信号量（S01-07）。
	manager.WithLimiter(limiter)
	singleSearcher := searchapp.NewSingleSourceSearcher(httpFetcher, scriptRuntime).WithLimiter(limiter)
	// 多源搜索/校验默认每源超时（READER_SEARCH_SOURCE_TIMEOUT，默认 15s）：input.Timeout<=0 时生效，
	// 避免单源 fetcher 长时间挂起拖垮整个搜索请求。
	multiSearcher := searchapp.NewMultiSourceSearcher(repo, singleSearcher).WithLimiter(limiter).WithSourceTimeout(cfg.SearchSourceTimeout)
	discoveryService := discoveryapp.NewService(repo, httpFetcher, scriptRuntime).WithLimiter(limiter)
	// 校验专用：取消全局并发上限（并发由 READER_CHECK_CONCURRENCY 控制），保留单源上限保护目标站点。
	checkLimiter := ratelimit.New(ratelimit.Options{Source: cfg.SourceConcurrency})
	checkSearcher := searchapp.NewMultiSourceSearcher(repo, searchapp.NewSingleSourceSearcher(httpFetcher, scriptRuntime).WithLimiter(checkLimiter)).WithLimiter(checkLimiter).WithSourceTimeout(cfg.SearchSourceTimeout)
	checkDiscovery := discoveryapp.NewService(repo, httpFetcher, scriptRuntime).WithLimiter(checkLimiter)
	// 书源导入/覆盖后失效两份探索分类缓存（搜索/发现共用与校验专用各一份），避免继续返回旧分类。
	importer.WithSavedCallback(func(u string) { discoveryService.InvalidateExploreKinds(u); checkDiscovery.InvalidateExploreKinds(u) })
	infoService := bookapp.NewInfoService(bookRepo, httpFetcher, scriptRuntime)
	tocService := bookapp.NewTocService(chapterRepo, httpFetcher, scriptRuntime).WithBooks(bookRepo)
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
	checkRunner := checkworker.NewRunner(checkRepo, checker).WithSourceLoader(repo).WithDiagnostics(checkapp.NewDiagnosticsUpdater(checkRepo)).WithEvents(checkEvents)
	checkManager := checkapp.NewManager(checkRepo, repo, checkRunner).WithEvents(checkEvents)
	cleanup = func() {
		// 释放空闲 keep-alive 连接（S03-15）：校验/任务收尾与 serve 优雅退出时归还连接池资源。
		httpFetcher.CloseIdleConnections()
		// 关闭校验事件发布器，订阅者事件流得以收敛。
		checkEvents.Close()
		_ = db.Close()
	}
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
