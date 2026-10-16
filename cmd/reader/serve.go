package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"reader/internal/api/generated"
	"reader/internal/api/handlers"
	"reader/internal/api/middleware"
	sourceapp "reader/internal/app/source"
	"reader/internal/bootstrap"
	"reader/internal/cli"
	"reader/internal/infra/config"

	"github.com/go-chi/chi/v5"

	_ "modernc.org/sqlite"
)

// HTTP server 超时：ReadTimeout 覆盖请求头+请求体读取，防 slowloris 慢速 body 长期占连接；
// IdleTimeout 回收 keep-alive 空闲连接。刻意不设全局 WriteTimeout：debug/check SSE 事件流
// 与多源搜索需要长时间写响应，全局写截止会把它们截断。
const (
	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 60 * time.Second
	serverIdleTimeout       = 120 * time.Second
	serverShutdownTimeout   = 5 * time.Second
)

// runServe 启动 HTTP API 服务；由 cli.Run dispatch（serve 是唯一需要 HTTP 的子命令）。
// ctx 在 SIGINT/SIGTERM 时取消，触发优雅关闭。日志为 JSON，输出到 stderr。
func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "", "listen address (overrides READER_ADDR, default 127.0.0.1:8080)")
	if code, done := cli.ParseFlags(fs, "serve [flags]", args, stdout, stderr); done {
		return code
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
	if err := config.RequireTokenForAddr(cfg.Addr, cfg.Token); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	logger.Info("reader config loaded", "config", cfg.Redacted())

	server, cleanup, err := newRuntimeServer(cfg)
	if err != nil {
		logger.Error("reader runtime init failed", "error", err)
		return 2
	}
	defer cleanup()

	logger.Info("reader server starting", "addr", server.Addr)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("reader server stopping", "cause", context.Cause(ctx).Error())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
		defer cancel()
		err := server.Shutdown(shutdownCtx)
		// Shutdown 后 ListenAndServe 立即返回；等待其退出再释放运行时。
		<-errCh
		if err != nil {
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

func newRuntimeServer(cfg config.Config) (*http.Server, func(), error) {
	rt, cleanup, err := bootstrap.Open(cfg)
	if err != nil {
		return nil, nil, err
	}
	apiHandler := handlers.NewSourceHandler(rt.Sources).WithImporter(rt.Importer).WithLogin(sourceapp.NewLoginService(rt.SourceRepo, sourceapp.NewScriptIntegration(rt.ScriptRuntime))).WithSearcher(rt.Searcher).WithDiscovery(rt.Discovery).WithBooks(rt.Books, rt.Content).WithImages(rt.ImageProxy).WithReplace(rt.Replace).WithDebug(rt.Debug, rt.DebugPublisher).WithCheck(rt.Check)
	router := chi.NewRouter()
	generated.HandlerFromMuxWithBaseURL(apiHandler, router, "/api/v1")
	handler := middleware.Trace(middleware.Auth(cfg.Token)(router))
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		IdleTimeout:       serverIdleTimeout,
	}, cleanup, nil
}
