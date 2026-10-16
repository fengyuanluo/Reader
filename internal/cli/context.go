package cli

import (
	"fmt"
	"io"
	"os"

	"reader/internal/bootstrap"
	"reader/internal/infra/config"
)

// cliTokenFallback 使 production 环境无 READER_TOKEN 时 config.Load 不阻断非 serve 子命令；
// CLI 直连数据库，不读取 token。若用户显式设置了 READER_TOKEN 则以用户值为准。
const cliTokenFallback = "cli-unused"

// openRuntime 装配 CLI 运行时；失败输出 error 并返回 2。
func openRuntime(stderr io.Writer) (*bootstrap.Runtime, func(), int) {
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
	rt, cleanup, err := bootstrap.Open(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return nil, nil, 2
	}
	return rt, cleanup, 0
}
