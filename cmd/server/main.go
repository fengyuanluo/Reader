package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"reader/internal/api/generated"
	"reader/internal/api/handlers"
	"reader/internal/api/middleware"
	bookapp "reader/internal/app/book"
	checkapp "reader/internal/app/check"
	contentapp "reader/internal/app/content"
	debugapp "reader/internal/app/debug"
	discoveryapp "reader/internal/app/discovery"
	replaceapp "reader/internal/app/replace"
	searchapp "reader/internal/app/search"
	sourceapp "reader/internal/app/source"
	"reader/internal/infra/config"
	"reader/internal/infra/fetcher"
	"reader/internal/infra/script"
	readersqlite "reader/internal/infra/sqlite"
	"reader/internal/infra/sqlite/repositories"
	checkworker "reader/internal/worker/check"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	_ "modernc.org/sqlite"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load(config.FromEnv())
	if err != nil {
		logger.Error("reader config failed", "error", err)
		os.Exit(1)
	}
	logger.Info("reader config loaded", "config", cfg.Redacted())

	server, cleanup, err := newRuntimeServer(cfg)
	if err != nil {
		logger.Error("reader runtime init failed", "error", err)
		os.Exit(1)
	}
	defer cleanup()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("reader server starting", "addr", server.Addr)
		errCh <- server.ListenAndServe()
	}()

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-signalCh:
		logger.Info("reader server stopping", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("reader server shutdown failed", "error", err)
			os.Exit(1)
		}
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("reader server failed", "error", err)
			os.Exit(1)
		}
	}
}

func newServer(addr string) *http.Server {
	return newServerWithToken(addr, "")
}

func newServerWithToken(addr string, token string) *http.Server {
	handler := middleware.Trace(middleware.Auth(token)(http.NewServeMux()))
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func newRuntimeServer(cfg config.Config) (*http.Server, func(), error) {
	if err := os.MkdirAll(filepath.Dir(cfg.SQLiteDSN), 0o755); err != nil && filepath.Dir(cfg.SQLiteDSN) != "." {
		return nil, nil, err
	}
	db, err := sql.Open("sqlite", cfg.SQLiteDSN)
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
	apiHandler := handlers.NewSourceHandler(manager).WithImporter(importer).WithSearcher(multiSearcher).WithDiscovery(discoveryService).WithBooks(bookService, contentApp).WithImages(imageProxy).WithReplace(replaceManager).WithDebug(debugService, debugPublisher).WithCheck(checkManager, cfg.GlobalConcurrency)
	router := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(apiHandler, router, "/api/v1")
	handler := middleware.Trace(middleware.Auth(cfg.Token)(router))
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}, cleanup, nil
}
