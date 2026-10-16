// Package cli 实现 reader 单二进制的非交互子命令。
//
// 退出码约定：0 成功；1 用法/参数错误；2 运行失败（config/db/网络/规则/部分章节下载失败）。
// 错误输出到 stderr，格式 `error: <msg>`；警告格式 `warn: <msg>`。
package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"reader/internal/bootstrap"
)

// cmdFunc 是单个子命令的执行形态；rt 由 Run 通过 bootstrap.Open 注入，
// serve 例外（自带 HTTP 装配，无需 rt）。
type cmdFunc func(rt *bootstrap.Runtime, args []string, stdout, stderr io.Writer) int

// ServeCommand 由 cmd/reader 注入，避免本包反向依赖 HTTP 装配。
var ServeCommand func(args []string, stdout, stderr io.Writer) int

const usageText = `usage: reader <command> [args]

commands:
  serve [--addr <addr>]              start HTTP API server (needs READER_TOKEN in production)
  import <path|URL|->                import book sources from file, URL, or stdin
  source list|show|enable|disable    manage imported book sources
  search <keyword>                   search enabled sources (or --source ids)
  book add|list|info|refresh|rm      manage bookshelf
  toc <bookId>                       list chapters of a book
  content <bookId> <index>           print chapter content
  download <bookId>                  download chapters into SQLite cache, export TXT

notes:
  add --json to any command for machine-readable JSON output.
  exit codes: 0 ok, 1 usage error, 2 runtime error.
`

// reorderArgs 把 flag 与位置参数混排的参数表规整为 flag 在前、位置参数在后。
// 标准库 flag 遇到首个位置参数即停止解析，而 CLI 约定 `download <id> --from 0`
// 与 `download --from 0 <id>` 等价。非 bool flag 向后吞一个取值（含负数如 -1）。
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	isBool := func(f *flag.Flag) bool {
		b, ok := f.Value.(interface{ IsBoolFlag() bool })
		return ok && b.IsBoolFlag()
	}
	var flagArgs, positional []string
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if tok == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(tok) > 1 && strings.HasPrefix(tok, "-") {
			name := strings.TrimLeft(tok, "-")
			if strings.Contains(name, "=") {
				flagArgs = append(flagArgs, tok)
				continue
			}
			f := fs.Lookup(name)
			flagArgs = append(flagArgs, tok)
			if f != nil && !isBool(f) && i+1 < len(args) {
				flagArgs = append(flagArgs, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, tok)
	}
	return append(flagArgs, positional...)
}

// parseCmdArgs 统一子命令参数解析；解析失败已写 stderr，返回非 nil。
func ParseArgs(fs *flag.FlagSet, args []string) error {
	return fs.Parse(reorderArgs(fs, args))
}

// Run 解析子命令并执行；无参数或未知子命令打印 usage 返回 1。
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usageText)
		return 1
	}
	name, rest := args[0], args[1:]
	if name == "serve" {
		if ServeCommand == nil {
			fmt.Fprintln(stderr, "error: serve command not available")
			return 1
		}
		return ServeCommand(rest, stdout, stderr)
	}
	var fn cmdFunc
	switch name {
	case "import":
		fn = runImport
	case "source":
		fn = runSource
	case "search":
		fn = runSearch
	case "book":
		fn = runBook
	case "toc":
		fn = runToc
	case "content":
		fn = runContent
	case "download":
		fn = runDownload
	default:
		fmt.Fprintf(stderr, "error: unknown command %q\n\n", name)
		_, _ = fmt.Fprint(stderr, usageText)
		return 1
	}
	rt, cleanup, code := openRuntime(stderr)
	if code != 0 {
		return code
	}
	defer cleanup()
	return fn(rt, rest, stdout, stderr)
}
