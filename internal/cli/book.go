package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"

	"reader/internal/domain/book"
)

const bookUsageText = `usage: reader book <subcommand> [args]

subcommands:
  add --source <sourceId> --url <bookUrl> [--name <n>] [--author <a>] [--no-refresh] [--json]
  list [--page 1] [--page-size 50] [--json]
  info <bookId> [--json]
  refresh <bookId> [--json]
  rm <bookId> [--json]

run "reader book <subcommand> --help" to list subcommand flags.
`

// runBook 实现 `reader book add|list|info|refresh|rm`。
func runBook(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, "error: book requires a subcommand\n\n"+bookUsageText)
		return 1
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return runBookAdd(env, rest, stdout, stderr)
	case "list":
		return runBookList(env, rest, stdout, stderr)
	case "info":
		return runBookInfo(env, rest, stdout, stderr)
	case "refresh":
		return runBookRefresh(env, rest, stdout, stderr)
	case "rm":
		return runBookRemove(env, rest, stdout, stderr)
	default:
		if isHelpArg(sub) {
			fmt.Fprint(stdout, bookUsageText)
			return 0
		}
		fmt.Fprintf(stderr, "error: unknown book subcommand %q\n\n%s", sub, bookUsageText)
		return 1
	}
}

func runBookAdd(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("book add", flag.ContinueOnError)
	sourceID := fs.String("source", "", "book source id (from search or source list)")
	bookURL := fs.String("url", "", "book url (bookUrl from search)")
	name := fs.String("name", "", "book name (refreshed later by default)")
	author := fs.String("author", "", "book author")
	noRefresh := fs.Bool("no-refresh", false, "skip info refresh after adding")
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "book add --source <sourceId> --url <bookUrl> [flags]", args, stdout, stderr); done {
		return code
	}
	if *sourceID == "" || *bookURL == "" {
		fmt.Fprintln(stderr, "error: book add requires --source <sourceId> and --url <bookUrl>")
		return 1
	}

	rt, code := env.runtime(stderr)
	if code != 0 {
		return code
	}
	ctx := env.ctx
	src, err := rt.SourceRepo.FindByID(ctx, *sourceID)
	if err != nil {
		fmt.Fprintf(stderr, "error: source %s: %v\n", *sourceID, err)
		return 2
	}
	b := &book.Book{
		BookURL:    *bookURL,
		Origin:     src.BookSourceURL,
		OriginName: src.BookSourceName,
		Name:       *name,
		Author:     *author,
	}
	if b.Name == "" {
		b.Name = *bookURL
	}
	saved, err := rt.Books.Save(ctx, b)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	bookID := book.IDFromURL(*bookURL)
	if !*noRefresh {
		refreshed, err := rt.Books.RefreshInfo(ctx, bookID)
		if err != nil {
			fmt.Fprintf(stderr, "warn: refresh info failed: %v\n", err)
		} else {
			saved = &refreshed
		}
	}
	if *asJSON {
		data, err := json.MarshalIndent(struct {
			BookID        string `json:"bookId"`
			Name          string `json:"name"`
			Author        string `json:"author"`
			OriginName    string `json:"originName"`
			LatestChapter string `json:"latestChapterTitle"`
		}{BookID: bookID, Name: saved.Name, Author: saved.Author, OriginName: saved.OriginName, LatestChapter: saved.LatestChapterTitle}, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	fmt.Fprintf(stdout, "added\t%s\t%s\t%s\t%s\n", bookID, saved.Name, saved.Author, saved.OriginName)
	return 0
}

func runBookList(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("book list", flag.ContinueOnError)
	page := fs.Int("page", 1, "page number (1-based)")
	pageSize := fs.Int("page-size", 50, "items per page")
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "book list [flags]", args, stdout, stderr); done {
		return code
	}
	rt, code := env.runtime(stderr)
	if code != 0 {
		return code
	}
	books, _, err := rt.Books.List(env.ctx, book.BookFilter{Page: *page, PageSize: *pageSize})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	type itemJSON struct {
		Index           int    `json:"index"`
		BookID          string `json:"bookId"`
		Name            string `json:"name"`
		Author          string `json:"author"`
		OriginName      string `json:"originName"`
		DurChapterIndex int    `json:"durChapterIndex"`
		LatestChapter   string `json:"latestChapter"`
	}
	out := struct {
		Items []itemJSON `json:"items"`
	}{}
	for i, b := range books {
		out.Items = append(out.Items, itemJSON{
			Index:           i + 1,
			BookID:          book.IDFromURL(b.BookURL),
			Name:            b.Name,
			Author:          b.Author,
			OriginName:      b.OriginName,
			DurChapterIndex: b.DurChapterIndex,
			LatestChapter:   b.LatestChapterTitle,
		})
	}
	if *asJSON {
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	for _, item := range out.Items {
		fmt.Fprintf(stdout, "%d\t%s\t%s\t%s\t%s\t%d\t%s\n",
			item.Index, item.BookID, item.Name, item.Author, item.OriginName, item.DurChapterIndex, item.LatestChapter)
	}
	return 0
}

func runBookInfo(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("book info", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "book info <bookId> [flags]", args, stdout, stderr); done {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "error: book info requires exactly one <bookId> argument")
		return 1
	}
	rt, code := env.runtime(stderr)
	if code != 0 {
		return code
	}
	b, err := rt.Books.Get(env.ctx, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if *asJSON {
		data, err := json.MarshalIndent(b, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	fmt.Fprintf(stdout, "name: %s\n", b.Name)
	fmt.Fprintf(stdout, "author: %s\n", b.Author)
	fmt.Fprintf(stdout, "originName: %s\n", b.OriginName)
	fmt.Fprintf(stdout, "wordCount: %s\n", b.WordCount)
	fmt.Fprintf(stdout, "tocUrl: %s\n", b.TocURL)
	fmt.Fprintf(stdout, "coverUrl: %s\n", b.CoverURL)
	fmt.Fprintf(stdout, "intro: %s\n", truncateRunes(b.Intro, 200))
	return 0
}

func runBookRefresh(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("book refresh", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "book refresh <bookId> [flags]", args, stdout, stderr); done {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "error: book refresh requires exactly one <bookId> argument")
		return 1
	}
	rt, code := env.runtime(stderr)
	if code != 0 {
		return code
	}
	ctx := env.ctx
	id := fs.Arg(0)
	refreshed, err := rt.Books.RefreshInfo(ctx, id)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	chapters, _, err := rt.Books.RefreshToc(ctx, id)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if *asJSON {
		data, err := json.MarshalIndent(struct {
			BookID        string `json:"bookId"`
			Name          string `json:"name"`
			LatestChapter string `json:"latestChapterTitle"`
			Chapters      int    `json:"chapters"`
		}{BookID: id, Name: refreshed.Name, LatestChapter: refreshed.LatestChapterTitle, Chapters: len(chapters)}, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	fmt.Fprintf(stdout, "refreshed\t%s\tchapters=%d\n", id, len(chapters))
	return 0
}

func runBookRemove(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("book rm", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "book rm <bookId> [flags]", args, stdout, stderr); done {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "error: book rm requires exactly one <bookId> argument")
		return 1
	}
	rt, code := env.runtime(stderr)
	if code != 0 {
		return code
	}
	id := fs.Arg(0)
	if err := rt.Books.Delete(env.ctx, id); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if *asJSON {
		data, err := json.MarshalIndent(struct {
			BookID string `json:"bookId"`
			Status string `json:"status"`
		}{BookID: id, Status: "removed"}, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	fmt.Fprintf(stdout, "removed\t%s\n", id)
	return 0
}

// runToc 实现 `reader toc <bookId>`。
func runToc(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("toc", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "toc <bookId> [flags]", args, stdout, stderr); done {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "error: toc requires exactly one <bookId> argument")
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
	type chapterJSON struct {
		Index    int    `json:"index"`
		Title    string `json:"title"`
		URL      string `json:"url"`
		IsVolume bool   `json:"isVolume"`
	}
	out := struct {
		BookID   string        `json:"bookId"`
		Name     string        `json:"name"`
		Count    int           `json:"count"`
		Chapters []chapterJSON `json:"chapters"`
	}{BookID: id, Name: b.Name, Count: len(chapters)}
	for _, c := range chapters {
		out.Chapters = append(out.Chapters, chapterJSON{Index: c.Index, Title: c.Title, URL: c.URL, IsVolume: c.IsVolume})
	}
	if *asJSON {
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	for _, c := range chapters {
		title := c.Title
		if c.IsVolume {
			title = "[卷] " + title
		}
		fmt.Fprintf(stdout, "%d\t%s\t%s\n", c.Index, title, c.URL)
	}
	return 0
}

// runContent 实现 `reader content <bookId> <index>`；文本模式 stdout 只输出正文原文。
func runContent(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("content", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "content <bookId> <index> [flags]", args, stdout, stderr); done {
		return code
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, "error: content requires <bookId> <index>")
		return 1
	}
	index, err := strconv.Atoi(fs.Arg(1))
	if err != nil || index < 0 {
		fmt.Fprintf(stderr, "error: chapter index must be a non-negative integer, got %q\n", fs.Arg(1))
		return 1
	}
	rt, code := env.runtime(stderr)
	if code != 0 {
		return code
	}
	ctx := env.ctx
	id := fs.Arg(0)
	result, _, err := rt.Content.GetChapterContent(ctx, id, index)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if *asJSON {
		data, err := json.MarshalIndent(struct {
			BookID  string `json:"bookId"`
			Index   int    `json:"index"`
			Title   string `json:"title"`
			Content string `json:"content"`
		}{BookID: id, Index: index, Title: result.Chapter.Title, Content: result.Content}, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	fmt.Fprintln(stdout, result.Content)
	return 0
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "..."
}
