package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	checkapp "reader/internal/app/check"
	"reader/internal/bootstrap"
	"reader/internal/domain/source"
	"reader/internal/domain/task"
)

// maxCheckConcurrency 防自我 DOS：--concurrency 越界报用法错误退出码 1。
const maxCheckConcurrency = 64

// runCheck 实现 `reader check <source-ids|--all>`：前台等待的校验任务，
// 走 bootstrap.Runtime.Check（与 HTTP API 同一 checkapp.Manager），支持 --json、
// --concurrency、--keyword、--timeout。根 ctx 在 SIGINT/SIGTERM 时取消，runner
// 收尾后以 cancelled 终态返回。
func runCheck(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	all := fs.Bool("all", false, "validate all enabled sources")
	concurrency := fs.Int("concurrency", checkapp.DefaultConcurrency, fmt.Sprintf("concurrent sources per check task (1-%d)", maxCheckConcurrency))
	keyword := fs.String("keyword", "", "search keyword for validation (default: source checkKeyWord)")
	timeout := fs.Duration("timeout", 0, "per-source total timeout for the whole check flow (0 = checker default)")
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "check <source-ids|--all> [flags]", args, stdout, stderr); done {
		return code
	}
	// 参数校验全部在打开运行时之前完成，保证用法错误不建库不跑迁移。
	if !*all && fs.NArg() != 1 {
		fmt.Fprintln(stderr, "error: check requires <source-ids> or --all")
		return 1
	}
	if *all && fs.NArg() > 0 {
		fmt.Fprintln(stderr, "error: --all does not take source ids")
		return 1
	}
	if *concurrency < 1 || *concurrency > maxCheckConcurrency {
		fmt.Fprintf(stderr, "error: --concurrency must be between 1 and %d, got %d\n", maxCheckConcurrency, *concurrency)
		return 1
	}
	if *timeout < 0 {
		fmt.Fprintf(stderr, "error: --timeout must be >= 0, got %s\n", *timeout)
		return 1
	}

	var ids []string
	if !*all {
		for _, id := range strings.Split(fs.Arg(0), ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			fmt.Fprintln(stderr, "error: check requires at least one sourceId")
			return 1
		}
	}

	rt, code := env.runtime(stderr)
	if code != 0 {
		return code
	}
	ctx := env.ctx
	if *all {
		sources, err := sourceListAllEnabled(ctx, rt)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 2
		}
		if len(sources) == 0 {
			fmt.Fprintln(stderr, "error: no enabled sources")
			return 2
		}
		for _, src := range sources {
			ids = append(ids, src.SourceID)
		}
	}

	opts := checkapp.Options{
		Keyword:        *keyword,
		CheckSearch:    true,
		CheckDiscovery: true,
		CheckInfo:      true,
		CheckToc:       true,
		CheckContent:   true,
		Timeout:        *timeout,
	}
	// 任务 ID 与 HTTP API 同一格式；TraceID 在 CLI 无请求上下文，留空。
	checkTask := &task.CheckTask{
		TaskID:    "check-" + time.Now().UTC().Format("20060102150405.000000000"),
		Status:    task.CheckTaskPending,
		CreatedAt: time.Now().UTC(),
	}
	if err := rt.Check.Start(ctx, checkTask, ids, opts, *concurrency); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	return waitCheckTask(env, rt, checkTask, *asJSON, stdout, stderr)
}

type checkItemJSON struct {
	SourceID       string `json:"sourceId"`
	BookSourceName string `json:"bookSourceName"`
	Status         string `json:"status"`
	FailureCode    string `json:"failureCode,omitempty"`
	Message        string `json:"message,omitempty"`
	DurationMS     int64  `json:"durationMs"`
}

// waitCheckTask 订阅事件流前台等待任务终态；事件流不可用或中断时退回轮询任务状态。
// 进度行（每条源开始/通过/失败）输出到 stderr，终态摘要输出到 stdout。
// 返回退出码：0 通过；2 失败或被取消。
func waitCheckTask(env *cmdEnv, rt *bootstrap.Runtime, created *task.CheckTask, asJSON bool, stdout, stderr io.Writer) int {
	start := time.Now()
	itemStatus := map[string]string{}
	printProgress := func(format string, args ...any) {
		fmt.Fprintf(stderr, format+"\n", args...)
	}

	// drainEvents 消费事件流直到终态事件或 ctx 取消；ok 表示收到终态。
	drainEvents := func(events <-chan *task.CheckEvent) bool {
		for event := range events {
			switch event.Type {
			case task.CheckEventItemStarted:
				itemStatus[event.SourceID] = "running"
				printProgress("[%d/%d] %s running", 0, created.Total, event.SourceID)
			case task.CheckEventItemPassed:
				itemStatus[event.SourceID] = string(task.CheckItemPassed)
				printProgress("passed\t%s\t%s", event.SourceID, event.Message)
			case task.CheckEventItemFailed:
				itemStatus[event.SourceID] = string(task.CheckItemFailed)
				printProgress("failed\t%s\t%s", event.SourceID, event.Message)
			case task.CheckEventCompleted, task.CheckEventCancelled:
				return true
			}
		}
		return false
	}

	terminal := false
	if events, ok := rt.Check.Subscribe(env.ctx, created.TaskID, 0); ok {
		terminal = drainEvents(events)
		if terminal {
			// 终态事件只携带摘要，任务对象仍是创建时快照（Items 空、Status=pending）：
			// 必须重新查库取最终任务状态，摘要才能反映 passed/failed（S03-12）。
			if current, err := rt.Check.Get(env.ctx, created.TaskID); err == nil {
				created = current
			}
		}
	}
	// 事件流不可用或被中断：轮询任务状态收尾，保证前台一定等到终态。
	for !terminal {
		if env.ctx.Err() != nil {
			// Ctrl-C：取消任务并等待 runner 落 cancelled 终态，再取最终状态。
			if _, _, err := rt.Check.Cancel(env.ctx, created.TaskID); err != nil {
				printProgress("cancel: %v", err)
			}
		}
		current, err := rt.Check.Get(env.ctx, created.TaskID)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 2
		}
		for _, item := range current.Items {
			if prev, ok := itemStatus[item.SourceID]; !ok || prev == "running" {
				if prev != string(item.Status) {
					printProgress("%s\t%s\t%s", item.Status, item.SourceID, item.BookSourceName)
				}
			}
			itemStatus[item.SourceID] = string(item.Status)
		}
		if current.Status == task.CheckTaskSucceeded || current.Status == task.CheckTaskFailed || current.Status == task.CheckTaskCancelled {
			created = current
			break
		}
		created = current
		select {
		case <-env.ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
	_ = start
	// FindTask 不再携带条目（S03-06 分页）：摘要前分页取回全部条目。
	for page := 1; ; page++ {
		items, total, err := rt.Check.ListItems(context.WithoutCancel(env.ctx), created.TaskID, nil, page, checkItemsPageSize)
		if err != nil {
			fmt.Fprintf(stderr, "error: list check items: %v\n", err)
			return 2
		}
		created.Items = append(created.Items, items...)
		if len(items) == 0 || len(created.Items) >= total {
			break
		}
	}
	return emitCheckSummary(created, asJSON, stdout, stderr)
}

const checkItemsPageSize = 500

// emitCheckSummary 输出终态摘要并返回退出码：0 通过，2 失败或取消。
func emitCheckSummary(created *task.CheckTask, asJSON bool, stdout, stderr io.Writer) int {
	passed, failed := 0, 0
	items := make([]checkItemJSON, 0, len(created.Items))
	for _, item := range created.Items {
		switch item.Status {
		case task.CheckItemPassed:
			passed++
		default:
			failed++
		}
		items = append(items, checkItemJSON{
			SourceID:       item.SourceID,
			BookSourceName: item.BookSourceName,
			Status:         string(item.Status),
			FailureCode:    item.FailureCode,
			Message:        item.Message,
			DurationMS:     item.TimeoutMS,
		})
	}
	if asJSON {
		out := struct {
			TaskID string          `json:"taskId"`
			Status string          `json:"status"`
			Total  int             `json:"total"`
			Passed int             `json:"passed"`
			Failed int             `json:"failed"`
			Items  []checkItemJSON `json:"items"`
		}{created.TaskID, string(created.Status), created.Total, passed, failed, items}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		for _, item := range items {
			if item.Status == string(task.CheckItemPassed) {
				fmt.Fprintf(stdout, "passed\t%s\t%s\n", item.SourceID, item.BookSourceName)
			} else {
				fmt.Fprintf(stdout, "failed\t%s\t%s\t%s\n", item.SourceID, item.BookSourceName, item.FailureCode)
			}
		}
		fmt.Fprintf(stdout, "total\t%d\npassed\t%d\nfailed\t%d\n", created.Total, passed, failed)
	}
	if failed > 0 || created.Status == task.CheckTaskCancelled {
		fmt.Fprintf(stderr, "error: check %s: %d passed, %d failed\n", created.Status, passed, failed)
		return 2
	}
	return 0
}

// checkAllPageSize 为 --all 分页拉取启用源时的分页大小。
const checkAllPageSize = 500

// sourceListAllEnabled 分页拉取全部启用源（--all 用），避免固定 PageSize 静默截断。
func sourceListAllEnabled(ctx context.Context, rt *bootstrap.Runtime) ([]*source.BookSource, error) {
	enabled := true
	filter := source.BookSourceFilter{Enabled: &enabled, PageSize: checkAllPageSize}
	var out []*source.BookSource
	for page := 1; ; page++ {
		filter.Page = page
		result, err := rt.Sources.List(ctx, filter)
		if err != nil {
			return nil, err
		}
		out = append(out, result.Items...)
		if len(result.Items) < checkAllPageSize || len(out) >= result.Total {
			return out, nil
		}
	}
}
