// Package cli 实现 reader 单二进制的非交互子命令。
//
// 退出码约定：0 成功；1 用法/参数错误；2 运行失败（config/db/网络/规则/部分章节下载失败/被信号中断）。
// 错误输出到 stderr，格式 `error: <msg>`；警告格式 `warn: <msg>`。
// 用法错误在打开数据库之前判定，不产生建目录/建库/迁移副作用。
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// cmdFunc 是单个子命令的执行形态；env 提供根 ctx 与延迟打开的运行时，
// serve 例外（自带 HTTP 装配）。
type cmdFunc func(env *cmdEnv, args []string, stdout, stderr io.Writer) int

// ServeCommand 由 cmd/reader 注入，避免本包反向依赖 HTTP 装配；ctx 在 SIGINT/SIGTERM 时取消。
var ServeCommand func(ctx context.Context, args []string, stdout, stderr io.Writer) int

const usageText = `usage: reader <command> [args]

commands:
  serve [--addr <addr>]              start HTTP API server (empty READER_TOKEN only on loopback)
  import <path|URL|->                import book sources from file, URL, or stdin
  source list|show|enable|disable    manage imported book sources
  search <keyword>                   search enabled sources (or --source ids)
  book add|list|info|refresh|rm      manage bookshelf
  toc <bookId>                       list chapters of a book
  content <bookId> <index>           print chapter content
  download <bookId>                  download chapters into SQLite cache, export TXT
  fetch <name>                       search, pick best source, download and export TXT
  help                               show this help

notes:
  run "reader <command> --help" to list command flags.
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

// ParseFlags 统一子命令参数解析：flag 与位置参数可混排，帮助与错误按双横线格式输出。
// done=true 表示调用方应立即以 code 返回：--help/-h 输出用法到 stdout 返回 0；
// 解析失败输出 error 与用法到 stderr 返回 1。
func ParseFlags(fs *flag.FlagSet, synopsis string, args []string, stdout, stderr io.Writer) (code int, done bool) {
	fs.SetOutput(io.Discard)
	err := fs.Parse(reorderArgs(fs, args))
	switch {
	case err == nil:
		return 0, false
	case errors.Is(err, flag.ErrHelp):
		writeFlagUsage(stdout, fs, synopsis)
		return 0, true
	default:
		fmt.Fprintf(stderr, "error: %v\n\n", err)
		writeFlagUsage(stderr, fs, synopsis)
		return 1, true
	}
}

// writeFlagUsage 以 `--name <type>` 形式列出 flag，与 docs/CLI.md 写法一致。
func writeFlagUsage(w io.Writer, fs *flag.FlagSet, synopsis string) {
	fmt.Fprintf(w, "usage: reader %s\n", synopsis)
	first := true
	fs.VisitAll(func(f *flag.Flag) {
		if first {
			fmt.Fprint(w, "\nflags:\n")
			first = false
		}
		typeName, usage := flag.UnquoteUsage(f)
		if typeName != "" {
			fmt.Fprintf(w, "  --%s <%s>\n", f.Name, typeName)
		} else {
			fmt.Fprintf(w, "  --%s\n", f.Name)
		}
		switch f.DefValue {
		case "", "0", "false":
			fmt.Fprintf(w, "      %s\n", usage)
		default:
			fmt.Fprintf(w, "      %s (default %s)\n", usage, f.DefValue)
		}
	})
}

// isHelpArg 判断参数是否为帮助请求。
func isHelpArg(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

// Run 解析子命令并执行；help 输出 usage 到 stdout 返回 0，无参数或未知子命令输出到 stderr 返回 1。
// 根 ctx 在首个 SIGINT/SIGTERM 时取消；之后恢复默认信号处理，再次 Ctrl-C 直接终止进程。
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usageText)
		return 1
	}
	name, rest := args[0], args[1:]
	if isHelpArg(name) {
		_, _ = fmt.Fprint(stdout, usageText)
		return 0
	}
	var fn cmdFunc
	switch name {
	case "serve":
		if ServeCommand == nil {
			fmt.Fprintln(stderr, "error: serve command not available")
			return 1
		}
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
	case "fetch":
		fn = runFetch
	default:
		fmt.Fprintf(stderr, "error: unknown command %q\n\n", name)
		_, _ = fmt.Fprint(stderr, usageText)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop)
	if fn == nil {
		return ServeCommand(ctx, rest, stdout, stderr)
	}
	env := &cmdEnv{ctx: ctx, open: openRuntime}
	defer env.close()
	return fn(env, rest, stdout, stderr)
}
