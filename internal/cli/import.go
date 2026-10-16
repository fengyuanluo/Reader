package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"reader/internal/bootstrap"
	sourceapp "reader/internal/app/source"
)

type importItemJSON struct {
	SourceID       string `json:"sourceId"`
	BookSourceURL  string `json:"bookSourceUrl"`
	BookSourceName string `json:"bookSourceName"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
}

type importRemoteErrorJSON struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

type importResultJSON struct {
	Added        int                    `json:"added"`
	Updated      int                    `json:"updated"`
	Skipped      int                    `json:"skipped"`
	Failed       int                    `json:"failed"`
	Items        []importItemJSON       `json:"items"`
	RemoteErrors []importRemoteErrorJSON `json:"remoteErrors"`
}

// runImport 实现 `reader import <path|URL|->`。
func runImport(rt *bootstrap.Runtime, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	keepName := fs.Bool("keep-name", false, "keep existing source name on conflict")
	keepGroup := fs.Bool("keep-group", false, "keep existing source group on conflict")
	keepEnabled := fs.Bool("keep-enabled", false, "keep existing enabled state on conflict")
	group := fs.String("group", "", "assign this group to imported sources")
	groupMode := fs.String("group-mode", "append", "group merge mode: append|replace")
	update := fs.String("update", "", "update policy for existing sources")
	asJSON := fs.Bool("json", false, "output JSON")
	if err := ParseArgs(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "error: import requires exactly one <path|URL|-> argument")
		return 1
	}
	if *groupMode != "append" && *groupMode != "replace" {
		fmt.Fprintf(stderr, "error: --group-mode must be append or replace, got %q\n", *groupMode)
		return 1
	}

	target := fs.Arg(0)
	input := sourceapp.ImportInput{Options: sourceapp.ImportOptions{
		KeepExistingName:    *keepName,
		KeepExistingGroup:   *keepGroup,
		KeepExistingEnabled: *keepEnabled,
		GroupMode:           *groupMode,
		GroupName:           *group,
		UpdatePolicy:        *update,
	}}
	switch {
	case target == "-":
		payload, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(stderr, "error: read stdin: %v\n", err)
			return 2
		}
		input.Payload = payload
	case strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"):
		input.RemoteURL = target
	default:
		payload, err := os.ReadFile(target)
		if err != nil {
			fmt.Fprintf(stderr, "error: read %s: %v\n", target, err)
			return 2
		}
		input.Payload = payload
	}

	result, err := rt.Importer.Import(context.Background(), input)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	out := importResultJSON{
		Added:   result.Added,
		Updated: result.Updated,
		Skipped: result.Skipped,
		Failed:  result.Failed,
	}
	for _, item := range result.Items {
		out.Items = append(out.Items, importItemJSON{
			SourceID:       item.SourceID,
			BookSourceURL:  item.BookSourceURL,
			BookSourceName: item.BookSourceName,
			Status:         string(item.Status),
			Error:          item.Error,
		})
	}
	for _, remote := range result.RemoteErrors {
		out.RemoteErrors = append(out.RemoteErrors, importRemoteErrorJSON{URL: remote.URL, Error: remote.Error})
	}

	if *asJSON {
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		for _, item := range result.Items {
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", item.Status, item.SourceID, item.BookSourceName)
		}
		for _, remote := range result.RemoteErrors {
			fmt.Fprintf(stderr, "warn: %s: %s\n", remote.URL, remote.Error)
		}
		fmt.Fprintf(stdout, "summary: added=%d updated=%d skipped=%d failed=%d\n", result.Added, result.Updated, result.Skipped, result.Failed)
	}
	// 逐条状态已表达失败项，导入本身成功即 exit 0。
	return 0
}
