package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"reader/internal/bootstrap"
	"reader/internal/domain/book"
)

type downloadFailureJSON struct {
	Index int    `json:"index"`
	Error string `json:"error"`
}

// downloadSummary 是 downloadChapters 的结果统计，与 download --json 的输出字段一致。
type downloadSummary struct {
	Downloaded int
	Cached     int
	Skipped    int
	Failed     []downloadFailureJSON
}

// runDownload 实现 `reader download <bookId>`：并发取正文写入 SQLite 缓存，默认导出 TXT。
// 缓存命中的章节不再发起抓取，重跑即断点续跑。
func runDownload(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	from := fs.Int("from", 0, "first chapter index (>= 0)")
	to := fs.Int("to", -1, "last chapter index (-1 = last chapter; larger values clamp to last)")
	exportPath := fs.String("export", "", "TXT export path (default downloads/<bookId>.txt)")
	noExport := fs.Bool("no-export", false, "skip TXT export")
	workers := fs.Int("workers", 4, "concurrent chapter downloads")
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "download <bookId> [flags]", args, stdout, stderr); done {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "error: download requires exactly one <bookId> argument")
		return 1
	}
	if *from < 0 {
		fmt.Fprintf(stderr, "error: --from must be >= 0, got %d\n", *from)
		return 1
	}
	if *to >= 0 && *to < *from {
		fmt.Fprintf(stderr, "error: --to %d is before --from %d\n", *to, *from)
		return 1
	}

	rt, code := env.runtime(stderr)
	if code != 0 {
		return code
	}
	ctx := env.ctx
	id := fs.Arg(0)
	chapters, b, err := rt.Books.ListChapters(ctx, id)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if len(chapters) == 0 {
		fmt.Fprintf(stderr, "error: toc is empty, run: reader book refresh %s\n", id)
		return 2
	}

	last := len(chapters) - 1
	if *from > last {
		fmt.Fprintf(stderr, "error: --from %d out of range (last chapter index %d)\n", *from, last)
		return 1
	}
	if *to < 0 || *to > last {
		*to = last
	}

	summary := struct {
		BookID     string                `json:"bookId"`
		From       int                   `json:"from"`
		To         int                   `json:"to"`
		Downloaded int                   `json:"downloaded"`
		Cached     int                   `json:"cached"`
		Skipped    int                   `json:"skipped"`
		Failed     []downloadFailureJSON `json:"failed"`
		Exported   string                `json:"exported"`
	}{BookID: id, From: *from, To: *to}

	sections, dlSummary := downloadChapters(ctx, rt, b, id, chapters, *from, *to, *workers, stderr)
	summary.Downloaded = dlSummary.Downloaded
	summary.Cached = dlSummary.Cached
	summary.Skipped = dlSummary.Skipped
	summary.Failed = dlSummary.Failed
	if summary.Failed == nil {
		// JSON 输出 "failed": [] 而非 null，机器消费方不需特判（S16-04）。
		summary.Failed = []downloadFailureJSON{}
	}

	// 被信号中断：已抓章节已入缓存可续跑，不导出以免残缺内容覆盖已有 TXT。
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "error: interrupted, export skipped; rerun to resume from cache")
		return 2
	}

	switch {
	case *noExport:
	case len(sections) == 0:
		// 无任何正文（全为卷章节或全部失败）时不导出，避免覆盖已有 TXT。
		fmt.Fprintln(stderr, "warn: no chapter content, export skipped")
	default:
		path := *exportPath
		if path == "" {
			path = filepath.Join("downloads", id+".txt")
		}
		if err := exportTXT(path, b.Name, b.Author, sections); err != nil {
			fmt.Fprintf(stderr, "error: export: %v\n", err)
			return 2
		}
		summary.Exported = path
	}

	exitCode := 0
	if len(summary.Failed) > 0 {
		exitCode = 2
	}
	if *asJSON {
		data, err := json.MarshalIndent(summary, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		for _, f := range summary.Failed {
			fmt.Fprintf(stdout, "failed\t%d\t%s\n", f.Index, f.Error)
		}
		fmt.Fprintf(stdout, "downloaded\t%d\n", summary.Downloaded)
		fmt.Fprintf(stdout, "cached\t%d\n", summary.Cached)
		fmt.Fprintf(stdout, "skipped\t%d\n", summary.Skipped)
		fmt.Fprintf(stdout, "failed\t%d\n", len(summary.Failed))
		fmt.Fprintf(stdout, "exported\t%s\n", summary.Exported)
	}
	if exitCode != 0 {
		fmt.Fprintf(stderr, "error: %d chapters failed\n", len(summary.Failed))
	}
	return exitCode
}

// downloadChapters 以 workers 个 goroutine 并发下载 [from,to] 区间内的章节，
// 失败章节再重试一轮；返回的 sections 按章节下标顺序排列，TXT 顺序与目录一致。
func downloadChapters(ctx context.Context, rt *bootstrap.Runtime, b *book.Book, id string, chapters []*book.BookChapter, from, to, workers int, stderr io.Writer) (sections []string, summary downloadSummary) {
	if workers < 1 {
		workers = 1
	}
	// 网络并发与 SQLite 单连接写库并不冲突；数据库/sql 会在 SetMaxOpenConns(1) 下排队写请求，
	// 所以这里用 goroutine + channel 自己实现“单章失败继续”，不使用 concurrency.Map。
	total := to - from + 1
	src, srcErr := rt.SourceRepo.FindByURL(ctx, b.Origin)
	if srcErr != nil || src == nil {
		// 书源查不到时本次下载无法发起任何网络请求：整个区间直接失败，不静默传 nil。
		msg := srcErr.Error()
		if srcErr == nil {
			msg = "source not found: " + b.Origin
		}
		summary.Failed = make([]downloadFailureJSON, 0, total)
		for index := from; index <= to; index++ {
			if !chapters[index].IsVolume {
				summary.Failed = append(summary.Failed, downloadFailureJSON{Index: index, Error: msg})
			} else {
				summary.Skipped++
			}
		}
		fmt.Fprintln(stderr, "error: download: "+msg)
		return nil, summary
	}
	bookID := book.IDFromURL(b.BookURL)

	var (
		mu         sync.Mutex
		wg         sync.WaitGroup
		result     = make([]string, total)
		failed     map[int]string
		downloaded int
		cached     int
		skipped    int
	)
	failed = make(map[int]string)

	// fetchOne 下载单个章节并写入 result 对应下标；失败记录在 failed。
	fetchOne := func(index int) {
		chapter := chapters[index]
		if cachedText, ok, err := rt.ChapterRepo.GetContent(ctx, bookID, index); err != nil {
			mu.Lock()
			failed[index] = err.Error()
			mu.Unlock()
			return
		} else if ok {
			// 缓存保存净化前正文，导出前同样要应用用户替换规则（见 contentapp.Service.ProcessContent）。
			processed, err := rt.Content.Content.ProcessContent(ctx, *b, *chapter, cachedText)
			if err != nil {
				mu.Lock()
				failed[index] = err.Error()
				mu.Unlock()
				return
			}
			mu.Lock()
			cached++
			result[index-from] = chapterSection(index, chapter.Title, processed)
			mu.Unlock()
			return
		}
		// 对齐 Legado BookContent.kt:52-56 与 contentapp.Service 的语义：
		// 按目录判断 index+1 是否存在（与 --to 无关），末章时回退到第 0 章 URL。
		nextChapterURL := ""
		if index+1 < len(chapters) {
			nextChapterURL = chapters[index+1].URL
		} else if len(chapters) > 0 {
			nextChapterURL = chapters[0].URL
		}
		res, err := rt.Content.GetChapterContentWith(ctx, src, *b, *chapter, nextChapterURL)
		if err != nil {
			mu.Lock()
			failed[index] = err.Error()
			mu.Unlock()
			return
		}
		// 已取消：结果记 cancelled，不再计数与输出，保证区间守恒（S16-05）。
		if ctx.Err() != nil {
			mu.Lock()
			failed[index] = "canceled"
			mu.Unlock()
			return
		}
		mu.Lock()
		downloaded++
		result[index-from] = chapterSection(index, chapter.Title, res.Content)
		mu.Unlock()
	}

	// runWave 启动 workers 个 goroutine 消费待下载章节下标。
	runWave := func(indexes []int) {
		queue := make(chan int)
		// 进度行序号按派发顺序递增，与串行版 [序号/总数] 一致，不受完成顺序影响。
		seq := 0
		wg.Add(workers)
		for range workers {
			go func() {
				defer wg.Done()
				for index := range queue {
					if ctx.Err() != nil {
						// 已取消：剩余章节记 cancelled，保证 Downloaded+Failed 区间守恒（S16-05）。
						mu.Lock()
						failed[index] = "canceled"
						mu.Unlock()
						continue
					}
					fetchOne(index)
					if ctx.Err() != nil {
						// 已取消：丢弃结果与进度行，与串行版“取消后停止”一致。
						continue
					}
					mu.Lock()
					seq++
					fmt.Fprintf(stderr, "[%d/%d] %d %s\n", seq, total, index, chapters[index].Title)
					mu.Unlock()
				}
			}()
		}
		// 卷章节直接计入 Skipped，不进入下载队列。
		pending := make([]int, 0, len(indexes))
		for _, index := range indexes {
			if chapters[index].IsVolume {
				mu.Lock()
				skipped++
				mu.Unlock()
				continue
			}
			pending = append(pending, index)
		}
		for _, index := range pending {
			if ctx.Err() != nil {
				// 已取消：未派发的章节记 cancelled，保证区间守恒（S16-05）。
				mu.Lock()
				failed[index] = "canceled"
				mu.Unlock()
				continue
			}
			queue <- index
		}
		close(queue)
		wg.Wait()
	}

	first := make([]int, 0, total)
	for index := from; index <= to; index++ {
		first = append(first, index)
	}
	runWave(first)

	// 失败章节用相同 workers 再重试一轮；仍失败的才计入 Failed（按 index 升序）。
	retry := make([]int, 0, len(failed))
	for index := range failed {
		retry = append(retry, index)
	}
	sort.Ints(retry)
	if len(retry) > 0 && ctx.Err() == nil {
		failed = make(map[int]string)
		runWave(retry)
	}

	for _, index := range retry {
		if msg, ok := failed[index]; ok {
			summary.Failed = append(summary.Failed, downloadFailureJSON{Index: index, Error: msg})
		}
	}

	summary.Downloaded = downloaded
	summary.Cached = cached
	summary.Skipped = skipped

	sections = make([]string, 0, total)
	for _, section := range result {
		if section != "" {
			sections = append(sections, section)
		}
	}
	return sections, summary
}

func chapterSection(index int, title, content string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n\n=== %d %s ===\n\n%s", index, title, strings.TrimRight(content, "\n"))
	return sb.String()
}

// exportTXT 原子写出 TXT：先写同目录临时文件，成功后 fsync 并 rename 替换目标，
// 写中途失败（磁盘满/信号/io 错误）不会截断或覆盖已有 TXT（S16-02）。
func exportTXT(path, name, author string, sections []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	// 写失败时清理临时文件，不触碰目标文件。
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmpPath)
		}
	}()
	if _, err = fmt.Fprintf(f, "%s\n%s\n\n", name, author); err != nil {
		return err
	}
	for _, section := range sections {
		if _, err = io.WriteString(f, section); err != nil {
			return err
		}
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// 同目录 rename，POSIX 原子替换目标文件。
	if err = os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}
