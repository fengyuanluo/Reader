package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	searchapp "reader/internal/app/search"
)

// runSearch 实现 `reader search <keyword>`；bookUrl + sourceId 是 book add 的稳定输入。
func runSearch(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	sourceIDs := fs.String("source", "", "comma-separated sourceIds (allows searching disabled sources)")
	group := fs.String("group", "", "search sources in this group")
	page := fs.Int("page", 1, "page number (1-based)")
	limit := fs.Int("limit", 20, "max results printed (>= 1)")
	precision := fs.Bool("precision", false, "only keep exact keyword matches")
	concurrency := fs.Int("concurrency", 0, "per-source search concurrency (0 = searcher default)")
	timeout := fs.Duration("timeout", 0, "per-source search timeout (0 = use default, currently 15s)")
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "search <keyword> [flags]", args, stdout, stderr); done {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "error: search requires exactly one <keyword> argument")
		return 1
	}
	if *limit < 1 {
		fmt.Fprintf(stderr, "error: --limit must be >= 1, got %d\n", *limit)
		return 1
	}
	if *timeout < 0 {
		fmt.Fprintf(stderr, "error: --timeout must be >= 0, got %s\n", *timeout)
		return 1
	}
	var ids []string
	if trimmed := strings.TrimSpace(*sourceIDs); trimmed != "" {
		for _, id := range strings.Split(trimmed, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	// --concurrency 在 config.Load 后覆盖 READER_GLOBAL_CONCURRENCY，保证设置真正生效。
	env.globalConcurrency = *concurrency
	rt, code := env.runtime(stderr)
	if code != 0 {
		return code
	}
	result, err := rt.Searcher.Search(env.ctx, searchapp.MultiSearchInput{
		Keyword:     fs.Arg(0),
		Page:        *page,
		SourceIDs:   ids,
		Group:       *group,
		EnabledOnly: len(ids) == 0,
		Precision:   *precision,
		Concurrency: *concurrency,
		Timeout:     *timeout,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	items := result.Items
	if len(items) > *limit {
		items = items[:*limit]
	}

	type itemJSON struct {
		Index         int    `json:"index"`
		Name          string `json:"name"`
		Author        string `json:"author"`
		BookURL       string `json:"bookUrl"`
		SourceID      string `json:"sourceId"`
		Origin        string `json:"origin"`
		OriginName    string `json:"originName"`
		LatestChapter string `json:"latestChapter"`
	}
	out := struct {
		Items    []itemJSON          `json:"items"`
		Warnings []searchWarningJSON `json:"warnings"`
	}{}
	sourceIDByOrigin := map[string]string{}
	for _, sb := range items {
		origin := sb.Origin
		if _, ok := sourceIDByOrigin[origin]; !ok {
			sourceIDByOrigin[origin] = ""
			if src, err := rt.SourceRepo.FindByURL(env.ctx, origin); err == nil && src != nil {
				sourceIDByOrigin[origin] = src.SourceID
			}
		}
	}
	for i, sb := range items {
		out.Items = append(out.Items, itemJSON{
			Index:         i + 1,
			Name:          sb.Name,
			Author:        sb.Author,
			BookURL:       sb.BookURL,
			SourceID:      sourceIDByOrigin[sb.Origin],
			Origin:        sb.Origin,
			OriginName:    sb.OriginName,
			LatestChapter: sb.LatestChapterTitle,
		})
	}
	for _, w := range result.Warnings {
		out.Warnings = append(out.Warnings, searchWarningJSON{
			SourceID:       w.SourceID,
			BookSourceName: w.BookSourceName,
			Code:           w.Code,
			Message:        w.Message,
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
	for _, w := range result.Warnings {
		fmt.Fprintf(stderr, "warn: %s: %s\n", w.BookSourceName, w.Message)
	}
	for _, item := range out.Items {
		fmt.Fprintf(stdout, "%d\t%s\t%s\t%s\t%s\n",
			item.Index, item.Name, item.Author, item.SourceID, item.LatestChapter)
	}
	return 0
}

type searchWarningJSON struct {
	SourceID       string `json:"sourceId"`
	BookSourceName string `json:"bookSourceName"`
	Code           string `json:"code"`
	Message        string `json:"message"`
}
