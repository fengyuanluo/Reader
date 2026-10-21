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
	"runtime/debug"
	"strconv"
	"strings"
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

// setGoMemoryLimit 在未设置 GOMEMLIMIT 环境变量时，按系统可用内存与 cfg.GoMemLimitRatio
// 设置 Go 软内存上限（runtime/debug.SetMemoryLimit），防止 2 万书源等大规模任务把进程内存推到
// 宿主机上限；显式 GOMEMLIMIT 优先。可用内存不可知（如 cgroup 限制读不到）时保持默认不动。
func setGoMemoryLimit(cfg config.Config) {
	if _, ok := os.LookupEnv("GOMEMLIMIT"); ok {
		return
	}
	available := availableMemoryBytes()
	if available <= 0 {
		return
	}
	limit := int64(float64(available) * cfg.GoMemLimitRatio)
	if limit > 0 {
		debug.SetMemoryLimit(limit)
	}
}

// availableMemoryBytes 返回本进程可用的内存字节数：cgroup v2/v3 上限与 /proc/meminfo 的
// MemAvailable 取较小者；均不可得时返回 0（调用方保持默认不动）。
func availableMemoryBytes() uint64 {
	available := uint64(0)
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if after, ok := strings.CutPrefix(line, "MemAvailable:"); ok {
				fields := strings.Fields(after)
				if len(fields) >= 2 && fields[1] == "kB" {
					if kb, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
						available = kb * 1024
					}
				}
				break
			}
		}
	}
	limit := cgroupMemoryLimitBytes()
	if limit > 0 && (available == 0 || limit < available) {
		available = limit
	}
	return available
}

// cgroupMemoryLimitBytes 读取 cgroup v2（memory.max）或 v3（memory.limit_in_bytes）的内存上限；
// "max"/不可读/无限大时返回 0。
func cgroupMemoryLimitBytes() uint64 {
	paths := []string{
		"/sys/fs/cgroup/memory.max",
		"/sys/fs/cgroup/memory/memory.limit_in_bytes",
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		value := strings.TrimSpace(string(data))
		if value == "" || value == "max" {
			continue
		}
		if limit, err := strconv.ParseUint(value, 10, 64); err == nil && limit < (1<<62) {
			return limit
		}
	}
	return 0
}

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
	setGoMemoryLimit(cfg)
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
			// 兜底：SSE 等活跃连接（防御性，事件流已在 cleanup 取消）使 Shutdown 超时时强制关闭，
			// 保证干净退出而不是超时失败。
			logger.Warn("reader server shutdown timeout, forcing close", "error", err)
			_ = server.Close()
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
	apiHandler := handlers.NewSourceHandler(rt.Sources).WithImporter(rt.Importer).WithLogin(sourceapp.NewLoginService(rt.SourceRepo, sourceapp.NewScriptIntegration(rt.ScriptRuntime))).WithSearcher(rt.Searcher).WithDiscovery(rt.Discovery).WithBooks(rt.Books, rt.Content).WithImages(rt.ImageProxy).WithReplace(rt.Replace).WithDebug(rt.Debug, rt.DebugPublisher).WithCheck(rt.Check).WithCheckConcurrency(cfg.CheckConcurrency).WithDebugBodyLimit(cfg.DebugBodyLimit)
	router := chi.NewRouter()
	// 查询参数绑定错误（?page=abc 等）走 ogen 的错误路径，默认返回 text/plain 400；
	// spec 声明所有 400 响应体为 application/json 的 Error 对象（code/message/traceId 必填），
	// 这里通过 HandlerWithOptions 注入 ErrorHandlerFunc 转 WriteError，traceId 由 Trace 中间件写入的 ctx 提供。
	generated.HandlerWithOptions(apiHandler, generated.ChiServerOptions{
		BaseURL:          "/api/v1",
		BaseRouter:       router,
		ErrorHandlerFunc: handlers.WriteParamError,
	})
	// SSE 事件流（/check/tasks/{id}/events、/debug/sessions/{id}/events）是活跃连接，
	// WithEventStreamShutdown 把它们的 context 纳入注册表，SIGTERM 后 CloseEventStreams 统一取消，
	// 避免活跃连接使优雅关闭必然超时（S10-01）。
	handler := apiHandler.WithEventStreamShutdown(middleware.Trace(middleware.Auth(cfg.Token)(router)))
	// cleanup 在 Shutdown 之后（含超时兜底 Close 之前）关闭全部 SSE 事件流，
	// 保证 SIGTERM 后进程退出码 0（S10-01）。
	streamCleanup := func() { apiHandler.CloseEventStreams() }
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		IdleTimeout:       serverIdleTimeout,
	}, func() {
		streamCleanup()
		cleanup()
	}, nil
}
