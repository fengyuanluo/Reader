package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"reader/internal/domain/book"
)

type downloadFailureJSON struct {
	Index int    `json:"index"`
	Error string `json:"error"`
}

// runDownload 实现 `reader download <bookId>`：逐章取正文写入 SQLite 缓存，默认导出 TXT。
// 缓存命中的章节不再发起抓取，重跑即断点续跑。
func runDownload(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	from := fs.Int("from", 0, "first chapter index (>= 0)")
	to := fs.Int("to", -1, "last chapter index (-1 = last chapter; larger values clamp to last)")
	exportPath := fs.String("export", "", "TXT export path (default downloads/<bookId>.txt)")
	noExport := fs.Bool("no-export", false, "skip TXT export")
	workers := fs.Int("workers", 1, "reserved; downloads run serially (SQLite single-conn)")
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "download <bookId> [flags]", args, stdout, stderr); done {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "error: download requires exactly one <bookId> argument")
		return 1
	}
	if *workers > 1 {
		fmt.Fprintln(stderr, "warn: --workers > 1 ignored, running serially")
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
	bookID := book.IDFromURL(b.BookURL)
	total := *to - *from + 1

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

	var sections []string
	for seq, index := 0, *from; index <= *to; seq, index = seq+1, index+1 {
		if ctx.Err() != nil {
			break
		}
		chapter := chapters[index]
		fmt.Fprintf(stderr, "[%d/%d] %d %s\n", seq+1, total, index, chapter.Title)
		if chapter.IsVolume {
			summary.Skipped++
			continue
		}
		if cached, ok, err := rt.ChapterRepo.GetContent(ctx, bookID, index); err != nil {
			summary.Failed = append(summary.Failed, downloadFailureJSON{Index: index, Error: err.Error()})
			continue
		} else if ok {
			// 缓存保存净化前正文，导出前同样要应用用户替换规则（见 contentapp.Service.ProcessContent）。
			processed, err := rt.Content.Content.ProcessContent(ctx, *b, *chapter, cached)
			if err != nil {
				summary.Failed = append(summary.Failed, downloadFailureJSON{Index: index, Error: err.Error()})
				continue
			}
			summary.Cached++
			sections = append(sections, chapterSection(index, chapter.Title, processed))
			continue
		}
		result, _, err := rt.Content.GetChapterContent(ctx, id, index)
		if err != nil {
			summary.Failed = append(summary.Failed, downloadFailureJSON{Index: index, Error: err.Error()})
			continue
		}
		summary.Downloaded++
		sections = append(sections, chapterSection(index, chapter.Title, result.Content))
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

func chapterSection(index int, title, content string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n\n=== %d %s ===\n\n%s", index, title, strings.TrimRight(content, "\n"))
	return sb.String()
}

func exportTXT(path, name, author string, sections []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n%s\n\n", name, author)
	for _, section := range sections {
		sb.WriteString(section)
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}
