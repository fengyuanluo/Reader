package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"reader/internal/domain/source"
)

const sourceUsageText = `usage: reader source <subcommand> [args]

subcommands:
  list [--keyword <kw>] [--group <g>] [--enabled true|false] [--explore-enabled true|false] [--page 1] [--page-size 50] [--json]
  show <sourceId> [--json]
  enable <sourceId> [--json]
  disable <sourceId> [--json]

run "reader source <subcommand> --help" to list subcommand flags.
`

// runSource 实现 `reader source list|show|enable|disable`。
func runSource(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, "error: source requires a subcommand\n\n"+sourceUsageText)
		return 1
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return runSourceList(env, rest, stdout, stderr)
	case "show":
		return runSourceShow(env, rest, stdout, stderr)
	case "enable":
		return runSourceSetEnabled(env, rest, true, stdout, stderr)
	case "disable":
		return runSourceSetEnabled(env, rest, false, stdout, stderr)
	default:
		if isHelpArg(sub) {
			fmt.Fprint(stdout, sourceUsageText)
			return 0
		}
		fmt.Fprintf(stderr, "error: unknown source subcommand %q\n\n%s", sub, sourceUsageText)
		return 1
	}
}

func runSourceList(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("source list", flag.ContinueOnError)
	keyword := fs.String("keyword", "", "filter by keyword in id/name/url/group")
	group := fs.String("group", "", "filter by group")
	enabled := fs.String("enabled", "", "filter by enabled: true|false")
	exploreEnabled := fs.String("explore-enabled", "", "filter by enabledExplore: true|false")
	page := fs.Int("page", 1, "page number (1-based)")
	pageSize := fs.Int("page-size", 50, "items per page")
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "source list [flags]", args, stdout, stderr); done {
		return code
	}
	filter := source.BookSourceFilter{
		Keyword:  *keyword,
		Group:    *group,
		Page:     *page,
		PageSize: *pageSize,
	}
	enabledPtr, code := parseBoolFlag(*enabled, "--enabled", stderr)
	if code != 0 {
		return code
	}
	filter.Enabled = enabledPtr
	explorePtr, code := parseBoolFlag(*exploreEnabled, "--explore-enabled", stderr)
	if code != 0 {
		return code
	}
	filter.EnabledExplore = explorePtr

	rt, code := env.runtime(stderr)
	if code != 0 {
		return code
	}
	result, err := rt.Sources.List(env.ctx, filter)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	type itemJSON struct {
		Index          int    `json:"index"`
		SourceID       string `json:"sourceId"`
		Name           string `json:"name"`
		Group          string `json:"group"`
		Enabled        bool   `json:"enabled"`
		EnabledExplore bool   `json:"enabledExplore"`
		HasSearchURL   bool   `json:"hasSearchUrl"`
	}
	out := struct {
		Total int        `json:"total"`
		Items []itemJSON `json:"items"`
	}{Total: result.Total}
	for i, src := range result.Items {
		out.Items = append(out.Items, itemJSON{
			Index:          i + 1,
			SourceID:       src.SourceID,
			Name:           src.BookSourceName,
			Group:          src.BookSourceGroup,
			Enabled:        src.Enabled,
			EnabledExplore: src.EnabledExplore,
			HasSearchURL:   src.SearchURL != "",
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
	for i, src := range result.Items {
		fmt.Fprintf(stdout, "%d\t%s\t%s\t%s\t%t\t%t\t%t\n",
			i+1, src.SourceID, src.BookSourceName,
			dashIfEmpty(src.BookSourceGroup), src.Enabled, src.EnabledExplore, src.SearchURL != "")
	}
	return 0
}

func runSourceShow(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("source show", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "source show <sourceId> [flags]", args, stdout, stderr); done {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "error: source show requires exactly one <sourceId> argument")
		return 1
	}
	rt, code := env.runtime(stderr)
	if code != 0 {
		return code
	}
	src, err := rt.Sources.Get(env.ctx, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if *asJSON {
		data, err := json.MarshalIndent(src, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	fmt.Fprintf(stdout, "name: %s\n", src.BookSourceName)
	fmt.Fprintf(stdout, "url: %s\n", src.BookSourceURL)
	fmt.Fprintf(stdout, "group: %s\n", src.BookSourceGroup)
	fmt.Fprintf(stdout, "enabled: %t\n", src.Enabled)
	fmt.Fprintf(stdout, "enabledExplore: %t\n", src.EnabledExplore)
	fmt.Fprintf(stdout, "searchUrl: %s\n", src.SearchURL)
	fmt.Fprintf(stdout, "lastUpdateTime: %d\n", src.LastUpdateTime)
	return 0
}

func runSourceSetEnabled(env *cmdEnv, args []string, enabled bool, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("source enable|disable", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "source enable|disable <sourceId> [flags]", args, stdout, stderr); done {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "error: source enable|disable requires exactly one <sourceId> argument")
		return 1
	}
	rt, code := env.runtime(stderr)
	if code != 0 {
		return code
	}
	id := fs.Arg(0)
	src, err := rt.Sources.UpdateEnabled(env.ctx, id, enabled)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if *asJSON {
		data, err := json.MarshalIndent(struct {
			SourceID string `json:"sourceId"`
			Enabled  bool   `json:"enabled"`
		}{SourceID: src.SourceID, Enabled: src.Enabled}, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	fmt.Fprintf(stdout, "%s\tenabled=%t\n", src.SourceID, src.Enabled)
	return 0
}

func parseBoolFlag(value, name string, stderr io.Writer) (*bool, int) {
	if value == "" {
		return nil, 0
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s must be true or false, got %q\n", name, value)
		return nil, 1
	}
	return &parsed, 0
}

func dashIfEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
