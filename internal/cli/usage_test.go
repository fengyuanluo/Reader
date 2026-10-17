package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reader/internal/bootstrap"
)

// noRuntimeEnv 返回打开运行时即判失败的环境，用于断言用法错误不触碰数据库。
func noRuntimeEnv(t *testing.T) *cmdEnv {
	t.Helper()
	return &cmdEnv{ctx: t.Context(), open: func(io.Writer, int) (*bootstrap.Runtime, func(), int) {
		t.Errorf("runtime must not be opened for usage errors")
		return nil, nil, 2
	}}
}

func TestRunHelpFlags(t *testing.T) {
	for _, arg := range []string{"--help", "-h", "help"} {
		t.Run(arg, func(t *testing.T) {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			if code := Run([]string{arg}, stdout, stderr); code != 0 {
				t.Fatalf("Run(%s) = %d, want 0", arg, code)
			}
			if !strings.Contains(stdout.String(), "usage: reader") || stderr.Len() != 0 {
				t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestSubcommandHelp(t *testing.T) {
	tests := []struct {
		args       []string
		wantCode   int
		wantStdout bool
		want       string
	}{
		{args: []string{"book", "--help"}, wantCode: 0, wantStdout: true, want: "add --source"},
		{args: []string{"book", "-h"}, wantCode: 0, wantStdout: true, want: "refresh <bookId>"},
		{args: []string{"book", "help"}, wantCode: 0, wantStdout: true, want: "rm <bookId>"},
		{args: []string{"book", "nope"}, wantCode: 1, want: "unknown book subcommand"},
		{args: []string{"book"}, wantCode: 1, want: "list [--page 1]"},
		{args: []string{"source", "--help"}, wantCode: 0, wantStdout: true, want: "show <sourceId>"},
		{args: []string{"source", "-h"}, wantCode: 0, wantStdout: true, want: "enable <sourceId>"},
		{args: []string{"source", "nope"}, wantCode: 1, want: "unknown source subcommand"},
		{args: []string{"source"}, wantCode: 1, want: "disable <sourceId>"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			fn := map[string]cmdFunc{"book": runBook, "source": runSource}[tt.args[0]]
			if code := fn(noRuntimeEnv(t), tt.args[1:], stdout, stderr); code != tt.wantCode {
				t.Fatalf("code = %d, want %d", code, tt.wantCode)
			}
			got, other := stderr, stdout
			if tt.wantStdout {
				got, other = stdout, stderr
			}
			if !strings.Contains(got.String(), tt.want) || other.Len() != 0 {
				t.Fatalf("output = %q (other %q), want %q", got.String(), other.String(), tt.want)
			}
		})
	}
}

func TestSubcommandFlagHelpUsesDoubleDash(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := runImport(noRuntimeEnv(t), []string{"--help"}, stdout, stderr); code != 0 {
		t.Fatalf("import --help = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{"usage: reader import <path|URL|-> [flags]", "--update <string>", "(default lastUpdateTime)", "--keep-name\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\n  -update") || stderr.Len() != 0 {
		t.Fatalf("help must use double dash only; stdout:\n%s\nstderr: %s", out, stderr.String())
	}

	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	if code := runToc(noRuntimeEnv(t), []string{"--bogus", "id"}, stdout, stderr); code != 1 {
		t.Fatalf("toc --bogus = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "error: flag provided but not defined: -bogus") || !strings.Contains(stderr.String(), "--json") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestUsageErrorsDoNotOpenRuntime(t *testing.T) {
	tests := []struct {
		name string
		fn   cmdFunc
		args []string
	}{
		{"toc no id", runToc, nil},
		{"content bad index", runContent, []string{"book_x", "x"}},
		{"content missing index", runContent, []string{"book_x"}},
		{"download no id", runDownload, nil},
		{"download negative from", runDownload, []string{"book_x", "--from", "-1"}},
		{"download to before from", runDownload, []string{"book_x", "--from", "5", "--to", "2"}},
		{"search no keyword", runSearch, nil},
		{"search zero limit", runSearch, []string{"kw", "--limit", "0"}},
		{"search negative limit", runSearch, []string{"kw", "--limit", "-1"}},
		{"import no target", runImport, nil},
		{"import bad update", runImport, []string{"f.json", "--update", "Always"}},
		{"import bad group mode", runImport, []string{"f.json", "--group-mode", "x"}},
		{"book add no source", runBook, []string{"add", "--url", "https://b"}},
		{"book info no id", runBook, []string{"info"}},
		{"book refresh no id", runBook, []string{"refresh"}},
		{"book rm no id", runBook, []string{"rm"}},
		{"source show no id", runSource, []string{"show"}},
		{"source enable no id", runSource, []string{"enable"}},
		{"source list bad bool", runSource, []string{"list", "--enabled", "maybe"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			if code := tt.fn(noRuntimeEnv(t), tt.args, stdout, stderr); code != 1 {
				t.Fatalf("code = %d, want 1; stderr: %s", code, stderr.String())
			}
			if !strings.HasPrefix(stderr.String(), "error: ") {
				t.Fatalf("stderr = %q, want error line", stderr.String())
			}
		})
	}
}

func TestRunUsageErrorCreatesNoDatabase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	t.Setenv("READER_SQLITE_DSN", filepath.Join(dir, "reader.db"))
	for _, args := range [][]string{{"toc"}, {"download", "book_x", "--from", "-1"}, {"book", "--help"}} {
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		Run(args, stdout, stderr)
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("Run(%v) created %s (err=%v)", args, dir, err)
		}
	}
}

func TestDownloadKeepsExistingExportWhenNothingToExport(t *testing.T) {
	env := newTestEnv(t)
	mock := newCLIMockSource(t)
	bookID := addMockBook(t, env, mock)
	exportPath := filepath.Join(t.TempDir(), "book.txt")
	const previous = "previous full export"

	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{name: "from out of range", args: []string{"--from", "99"}, wantCode: 1, wantErr: "out of range"},
		{name: "volume only range", args: []string{"--from", "1", "--to", "1"}, wantCode: 0, wantErr: "export skipped"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(exportPath, []byte(previous), 0o644); err != nil {
				t.Fatal(err)
			}
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			args := append([]string{bookID, "--export", exportPath}, tt.args...)
			if code := runDownload(env, args, stdout, stderr); code != tt.wantCode {
				t.Fatalf("runDownload() = %d, want %d; stderr: %s", code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantErr) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.wantErr)
			}
			if data, _ := os.ReadFile(exportPath); string(data) != previous {
				t.Fatalf("existing export overwritten: %q", data)
			}
		})
	}
}

func TestDownloadStopsOnCanceledContext(t *testing.T) {
	env := newTestEnv(t)
	mock := newCLIMockSource(t)
	bookID := addMockBook(t, env, mock)
	ctx, cancel := context.WithCancel(t.Context())
	env.ctx = ctx
	mock.onContent1 = cancel
	exportPath := filepath.Join(t.TempDir(), "book.txt")
	const previous = "previous full export"
	if err := os.WriteFile(exportPath, []byte(previous), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := runDownload(env, []string{bookID, "--export", exportPath}, stdout, stderr); code != 2 {
		t.Fatalf("runDownload() = %d, want 2; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "interrupted") {
		t.Fatalf("stderr = %q, want interrupted", stderr.String())
	}
	if strings.Contains(stderr.String(), "第二章") {
		t.Fatalf("download continued after cancel: %s", stderr.String())
	}
	if data, _ := os.ReadFile(exportPath); string(data) != previous {
		t.Fatalf("existing export overwritten: %q", data)
	}
}

func TestImportFileWithBOM(t *testing.T) {
	env := newTestEnv(t)
	mock := newCLIMockSource(t)
	path := filepath.Join(t.TempDir(), "sources.json")
	payload := "\xef\xbb\xbf[" + mock.sourceJSON() + "]"
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := runImport(env, []string{path}, stdout, stderr); code != 0 {
		t.Fatalf("runImport(BOM file) = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "summary: added=1") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
