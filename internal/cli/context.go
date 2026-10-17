package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"reader/internal/bootstrap"
	"reader/internal/infra/config"
)

// cliTokenFallback 使 production 环境无 READER_TOKEN 时 config.Load 不阻断非 serve 子命令；
// CLI 直连数据库，不读取 token。若用户显式设置了 READER_TOKEN 则以用户值为准。
const cliTokenFallback = "cli-unused"

// cmdEnv 是子命令执行环境：根 ctx（SIGINT/SIGTERM 时取消）与延迟打开的运行时。
// 子命令必须先完成全部参数校验再调用 runtime，保证用法错误不会建目录、建库或跑迁移。
type cmdEnv struct {
	ctx context.Context
	// open 的第二个参数为 CLI 显式指定的全局并发数（0 表示不覆盖配置）。
	open              func(stderr io.Writer, globalConcurrency int) (*bootstrap.Runtime, func(), int)
	rt                *bootstrap.Runtime
	cleanup           func()
	globalConcurrency int
}

// runtime 首次调用时打开运行时并缓存；失败已写 stderr，返回非 0 退出码。
func (e *cmdEnv) runtime(stderr io.Writer) (*bootstrap.Runtime, int) {
	if e.rt != nil {
		return e.rt, 0
	}
	rt, cleanup, code := e.open(stderr, e.globalConcurrency)
	if code != 0 {
		return nil, code
	}
	e.rt, e.cleanup = rt, cleanup
	return rt, 0
}

// close 释放已打开的运行时；未打开时无操作。
func (e *cmdEnv) close() {
	if e.cleanup != nil {
		e.cleanup()
	}
}

// openRuntime 装配 CLI 运行时；globalConcurrency > 0 时在 config.Load 后覆盖全局并发上限。
// 失败输出 error 并返回 2。
func openRuntime(stderr io.Writer, globalConcurrency int) (*bootstrap.Runtime, func(), int) {
	cfg, err := config.Load(config.SourceFunc(func(key string) (string, bool) {
		if value, ok := os.LookupEnv(key); ok {
			return value, ok
		}
		if key == "READER_TOKEN" {
			return cliTokenFallback, true
		}
		return "", false
	}))
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return nil, nil, 2
	}
	if globalConcurrency > 0 {
		cfg.GlobalConcurrency = globalConcurrency
	}
	rt, cleanup, err := bootstrap.Open(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return nil, nil, 2
	}
	return rt, cleanup, 0
}
