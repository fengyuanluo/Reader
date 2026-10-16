package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"reader/internal/api/generated"
	"reader/internal/api/handlers"
	"reader/internal/api/middleware"
	"reader/internal/bootstrap"
	"reader/internal/cli"
	"reader/internal/infra/config"

	"github.com/go-chi/chi/v5"

	_ "modernc.org/sqlite"
)

// runServe 启动 HTTP API 服务；由 cli.Run dispatch（serve 是唯一需要 HTTP 的子命令）。
func runServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "", "listen address (overrides READER_ADDR)")
	if err := cli.ParseArgs(fs, args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "error: unexpected argument %q\n", fs.Arg(0))
		return 1
	}
	cfg, err := config.Load(config.FromEnv())
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if *addr != "" {
		cfg.Addr = *addr
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	logger.Info("reader config loaded", "config", cfg.Redacted())

	server, cleanup, err := newRuntimeServer(cfg)
	if err != nil {
		logger.Error("reader runtime init failed", "error", err)
		return 2
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
			return 2
		}
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("reader server failed", "error", err)
			return 2
		}
	}
	return 0
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
	rt, cleanup, err := bootstrap.Open(cfg)
	if err != nil {
		return nil, nil, err
	}
	apiHandler := handlers.NewSourceHandler(rt.Sources).WithImporter(rt.Importer).WithSearcher(rt.Searcher).WithDiscovery(rt.Discovery).WithBooks(rt.Books, rt.Content).WithImages(rt.ImageProxy).WithReplace(rt.Replace).WithDebug(rt.Debug, rt.DebugPublisher).WithCheck(rt.Check)
	router := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(apiHandler, router, "/api/v1")
	handler := middleware.Trace(middleware.Auth(cfg.Token)(router))
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}, cleanup, nil
}
