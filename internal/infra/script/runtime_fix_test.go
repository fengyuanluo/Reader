package script

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"reader/internal/domain/fetch"
	"reader/internal/domain/source"
	readersqlite "reader/internal/infra/sqlite"
	"reader/internal/infra/sqlite/repositories"

	_ "modernc.org/sqlite"
)

// recordingFetcher 记录请求并返回固定响应。
type recordingFetcher struct {
	mu       sync.Mutex
	requests []fetch.Request
	body     []byte
}

func (f *recordingFetcher) Fetch(_ context.Context, req fetch.Request) (*fetch.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	body := f.body
	if body == nil {
		body = []byte("ok")
	}
	return &fetch.Response{StatusCode: 200, Body: body, URL: req.URL}, nil
}

func (f *recordingFetcher) last(t *testing.T) fetch.Request {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("no request recorded")
	}
	return f.requests[len(f.requests)-1]
}

func (f *recordingFetcher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func TestRuntimeConcurrentEvalSharesInitializedState(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "https://source.example.test"})
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			script := fmt.Sprintf(`cache.put("k%d", "v"); cookie.setCookie("https://source.example.test", "a%d=1"); java.put("p%d", "x"); cache.get("k%d")`, i, i, i, i)
			got, err := runtime.Eval(context.Background(), script, nil)
			if err != nil || got != "v" {
				t.Errorf("Eval() = %#v, %v", got, err)
			}
		}()
	}
	wg.Wait()
}

func TestRuntimeScriptRequestOptions(t *testing.T) {
	tests := []struct {
		name   string
		script string
		check  func(t *testing.T, req fetch.Request)
	}{
		{
			name:   "ajax object body is sent as JSON",
			script: `java.ajax('https://api.example.test/s,{"method":"POST","body":{"key":"斗破","page":1}}')`,
			check: func(t *testing.T, req fetch.Request) {
				if req.Method != "POST" || string(req.Body) != `{"key":"斗破","page":1}` {
					t.Fatalf("req = %+v body=%s", req, req.Body)
				}
			},
		},
		{
			name:   "ajax array body is sent as JSON",
			script: `java.ajax('https://api.example.test/s,{"method":"POST","body":[1,2]}')`,
			check: func(t *testing.T, req fetch.Request) {
				if string(req.Body) != `[1,2]` {
					t.Fatalf("body = %s", req.Body)
				}
			},
		},
		{
			name:   "type requests hex body and origin sets Origin header",
			script: `java.ajax('https://api.example.test/f,{"type":"zip","origin":"https://origin.example.test"}')`,
			check: func(t *testing.T, req fetch.Request) {
				if !req.EncodeBodyAsHex {
					t.Fatalf("req = %+v", req)
				}
			},
		},
		{
			name:   "proxy and dnsIp map to request fields",
			script: `java.ajax('https://api.example.test/f,{"proxy":"socks5://127.0.0.1:1080","dnsIp":"1.2.3.4"}')`,
			check: func(t *testing.T, req fetch.Request) {
				if req.Proxy != "socks5://127.0.0.1:1080" || req.DNSIP != "1.2.3.4" {
					t.Fatalf("req = %+v", req)
				}
			},
		},
		{
			name:   "head sends HEAD",
			script: `java.head("https://api.example.test/h", {}).code()`,
			check: func(t *testing.T, req fetch.Request) {
				if req.Method != "HEAD" {
					t.Fatalf("method = %s", req.Method)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := &recordingFetcher{}
			runtime := NewRuntime(Policy{Fetcher: fetcher})
			if _, err := runtime.Eval(context.Background(), tt.script, nil); err != nil {
				t.Fatalf("Eval() error = %v", err)
			}
			tt.check(t, fetcher.last(t))
		})
	}
}

func TestRuntimeJavaExtensions(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   any
	}{
		{"timeFormat", `java.timeFormat(0) === "` + time.UnixMilli(0).Local().Format("2006/01/02 15:04") + `"`, true},
		{"toNumChapter chinese", `java.toNumChapter("第一千零二十五章 大结局")`, "第1025章 大结局"},
		{"toNumChapter no match", `java.toNumChapter("序章")`, "序章"},
		{"strToBytes/bytesToStr gbk", `java.bytesToStr(java.strToBytes("中文", "gbk"), "gbk") + java.strToBytes("中", "gbk").length`, "中文2"},
		{"base64DecodeToByteArray", `java.base64DecodeToByteArray("AQID").length`, int64(3)},
		{"base64Decode charset", `java.base64Decode("1tDOxA==", "gbk")`, "中文"},
		{"base64Encode default no wrap", `java.base64Encode("abc")`, "YWJj"},
		{"hexDecodeToByteArray", `java.hexDecodeToByteArray("0a0b").length`, int64(2)},
		{"md5Encode16", `java.md5Encode16("abc")`, "3cd24fb0d6963f7d"},
		// java.net.URL.getHost() 不含端口，origin 含端口（JsURL.kt）。
		{"toURL", `var u = java.toURL("/p/a?x=1&y=%E4%B8%AD", "https://h.example.test:8443/b/"); [u.host, u.origin, u.pathname, u.searchParams.y].join("|")`, "h.example.test|https://h.example.test:8443|/p/a|中"},
		{"androidId", `java.androidId()`, readerAndroidID},
		{"t2s", `java.t2s("繁體")`, "繁体"},
		{"unsupported webView is catchable", `try { java.webView(null, "https://x.test", null); "no" } catch (e) { String(e).indexOf("script_unsupported") >= 0 }`, true},
		{"unsupported rar", `try { java.getRarStringContent("00", "a"); "no" } catch (e) { String(e).indexOf("script_unsupported") >= 0 }`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := NewRuntime(Policy{SourceKey: "src"})
			got, err := runtime.Eval(context.Background(), tt.script, nil)
			if err != nil {
				t.Fatalf("Eval() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("got = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestRuntimeCacheFileAndImportScript(t *testing.T) {
	fetcher := &recordingFetcher{body: []byte(`function lib(){ return "lib-ok"; }`)}
	runtime := NewRuntime(Policy{SourceKey: "src", Fetcher: fetcher})
	for range 2 {
		got, err := runtime.Eval(context.Background(), `eval(java.importScript("https://cdn.example.test/lib.js")); lib()`, nil)
		if err != nil {
			t.Fatalf("Eval() error = %v", err)
		}
		if got != "lib-ok" {
			t.Fatalf("got = %#v", got)
		}
	}
	if fetcher.count() != 1 {
		t.Fatalf("fetch count = %d, want 1 (cached)", fetcher.count())
	}
	if _, err := runtime.Eval(context.Background(), `java.importScript("local.js")`, nil); !errors.Is(err, ErrScriptFailed) || !strings.Contains(err.Error(), "script_unsupported") {
		t.Fatalf("local importScript without file root error = %v", err)
	}
}

func TestRuntimeFileSandbox(t *testing.T) {
	root := t.TempDir()
	fetcher := &recordingFetcher{body: buildZip(t, map[string]string{"dir/a.txt": "A", "dir/b.txt": "B"})}
	runtime := NewRuntime(Policy{SourceKey: "src", Fetcher: fetcher, FileRoot: root})
	got, err := runtime.Eval(context.Background(), `
var p = java.downloadFile("https://cdn.example.test/pack.zip");
var dir = java.unzipFile(p);
var text = java.readTxtFile(dir + "/dir/a.txt");
var exists = java.getFile(p).exists();
var all = java.getTxtInFolder(dir + "/dir");
[p, text, exists, all, java.getZipStringContent("https://cdn.example.test/pack.zip", "dir/b.txt"), java.deleteFile(p), java.getFile(p).exists(), java.readFile("missing") === null].join("|")
`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	wantPath := "/" + md5Encode16("https://cdn.example.test/pack.zip") + ".zip"
	if got != wantPath+"|A|true|A\nB|B|true|false|true" {
		t.Fatalf("got = %#v", got)
	}
	for _, escape := range []string{"../outside.txt", "/../../etc/passwd", "a/../../x"} {
		_, err := runtime.Eval(context.Background(), fmt.Sprintf(`java.readTxtFile(%q)`, escape), nil)
		if err == nil || !strings.Contains(err.Error(), "非法路径") {
			t.Fatalf("readTxtFile(%q) error = %v, want illegal path", escape, err)
		}
	}
	noRoot := NewRuntime(Policy{})
	if _, err := noRoot.Eval(context.Background(), `java.readTxtFile("a.txt")`, nil); err == nil || !strings.Contains(err.Error(), "script_unsupported") {
		t.Fatalf("no root error = %v", err)
	}
}

func TestFileSandboxRejectsZipSlipAndOversizedArchives(t *testing.T) {
	root := filepath.Join(t.TempDir(), "files")
	sandbox := newFileSandbox(root)
	if _, err := sandbox.write("slip.zip", buildZip(t, map[string]string{"../../evil.txt": "x"})); err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.unzip("slip.zip"); err == nil {
		t.Fatal("zip slip archive extracted")
	}
	if _, err := os.Stat(filepath.Join(root, "evil.txt")); !os.IsNotExist(err) {
		t.Fatalf("zip slip escaped extraction dir: %v", err)
	}
	var big bytes.Buffer
	w := zip.NewWriter(&big)
	entry, err := w.Create("big.bin")
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for range maxArchiveBytes/len(chunk) + 1 {
		_, _ = entry.Write(chunk)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.write("big.zip", big.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.unzip("big.zip"); err == nil {
		t.Fatal("oversized archive extracted")
	}
}

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range names {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(files[name]))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// buildTestTTF 构造最小 TrueType 字体：每个码点对应一个由给定坐标增量组成的单轮廓字形。
func buildTestTTF(glyphs map[rune][][2]int16) []byte {
	codes := make([]rune, 0, len(glyphs))
	for code := range glyphs {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	be := binary.BigEndian
	var glyf bytes.Buffer
	loca := []uint32{0, 0} // glyph 0 (.notdef) 为空
	for _, code := range codes {
		points := glyphs[code]
		g := be.AppendUint16(nil, 1)
		g = append(g, make([]byte, 8)...)
		g = be.AppendUint16(g, uint16(len(points)-1))
		g = be.AppendUint16(g, 0)
		for range points {
			g = append(g, 0x01)
		}
		for _, p := range points {
			g = be.AppendUint16(g, uint16(p[0]))
		}
		for _, p := range points {
			g = be.AppendUint16(g, uint16(p[1]))
		}
		for len(g)%2 != 0 {
			g = append(g, 0)
		}
		glyf.Write(g)
		loca = append(loca, uint32(glyf.Len()))
	}
	segCount := len(codes) + 1
	sub := be.AppendUint16(nil, 4)
	sub = be.AppendUint16(sub, uint16(16+segCount*8))
	sub = be.AppendUint16(sub, 0)
	sub = be.AppendUint16(sub, uint16(segCount*2))
	sub = append(sub, make([]byte, 6)...)
	for _, code := range codes {
		sub = be.AppendUint16(sub, uint16(code))
	}
	sub = be.AppendUint16(sub, 0xFFFF)
	sub = be.AppendUint16(sub, 0)
	for _, code := range codes {
		sub = be.AppendUint16(sub, uint16(code))
	}
	sub = be.AppendUint16(sub, 0xFFFF)
	for i, code := range codes {
		sub = be.AppendUint16(sub, uint16(int32(i+1)-int32(code)))
	}
	sub = be.AppendUint16(sub, 1)
	sub = append(sub, make([]byte, segCount*2)...)
	cmap := be.AppendUint16(nil, 0)
	cmap = be.AppendUint16(cmap, 1)
	cmap = be.AppendUint16(cmap, 3)
	cmap = be.AppendUint16(cmap, 1)
	cmap = be.AppendUint32(cmap, 12)
	cmap = append(cmap, sub...)
	head := make([]byte, 54)
	be.PutUint16(head[50:], 1) // indexToLocFormat=1：32 位 loca
	maxp := be.AppendUint32(nil, 0x00005000)
	maxp = be.AppendUint16(maxp, uint16(len(codes)+1))
	var locaBytes []byte
	for _, offset := range loca {
		locaBytes = be.AppendUint32(locaBytes, offset)
	}
	tables := []struct {
		tag  string
		data []byte
	}{{"cmap", cmap}, {"glyf", glyf.Bytes()}, {"head", head}, {"loca", locaBytes}, {"maxp", maxp}}
	out := be.AppendUint32(nil, 0x00010000)
	out = be.AppendUint16(out, uint16(len(tables)))
	out = append(out, make([]byte, 6)...)
	offset := 12 + len(tables)*16
	var body []byte
	for _, table := range tables {
		out = append(out, table.tag...)
		out = be.AppendUint32(out, 0)
		out = be.AppendUint32(out, uint32(offset+len(body)))
		out = be.AppendUint32(out, uint32(len(table.data)))
		body = append(body, table.data...)
		for len(body)%4 != 0 {
			body = append(body, 0)
		}
	}
	return append(out, body...)
}

func TestRuntimeQueryTTFAndReplaceFont(t *testing.T) {
	square := [][2]int16{{0, 0}, {100, 0}, {0, 100}, {-100, 0}}
	triangle := [][2]int16{{0, 0}, {50, 100}, {50, -100}}
	errorFont := buildTestTTF(map[rune][][2]int16{0xE000: square, 0xE001: triangle})
	correctFont := buildTestTTF(map[rune][][2]int16{'好': square, '的': triangle})
	runtime := NewRuntime(Policy{})
	got, err := runtime.EvalWithBindings(context.Background(), `
var bad = java.queryTTF(java.hexDecodeToByteArray(errHex));
var good = java.queryTTF(goodB64);
[java.replaceFont("\uE000\uE001x", bad, good), bad.getGlyfIdByUnicode(0xE001), good.getUnicodeByGlyf(bad.getGlyfByUnicode(0xE000)) === 0x597D, bad.getGlyfByUnicode(0x41) === null].join("|")
`, Bindings{"errHex": hex.EncodeToString(errorFont), "goodB64": base64EncodeFlags(correctFont, base64FlagNoWrap)})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "好的x|2|true|true" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeErrorCarriesStageAndSourceID(t *testing.T) {
	src := &source.BookSource{SourceID: "src_abc", BookSourceURL: "https://source.example.test"}
	runtime := NewRuntime(Policy{})
	ctx := WithSubStage(WithStage(context.Background(), "toc"), "format_js")
	_, err := runtime.EvalWithBindings(ctx, `throw new Error("boom")`, Bindings{"source": src})
	if !errors.Is(err, ErrScriptFailed) || !strings.HasPrefix(err.Error(), "script_failed: stage=toc.format_js source=src_abc: ") {
		t.Fatalf("error = %v", err)
	}
	_, err = runtime.Eval(context.Background(), `throw new Error("boom")`, nil)
	if err == nil || !strings.Contains(err.Error(), "stage=unknown") {
		t.Fatalf("unstaged error = %v", err)
	}
}

func TestRuntimeSourceBindingsGetPutAndFlags(t *testing.T) {
	src := &source.BookSource{BookSourceURL: "https://source.example.test", Enabled: true, EnabledCookieJar: true}
	runtime := NewRuntime(Policy{})
	got, err := runtime.EvalWithBindings(context.Background(), `
source.put("token", "t1");
[source.get("token"), source.get("missing"), source.enabled, source.enabledCookieJar].join("|")
`, Bindings{"source": src})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "t1||true|true" {
		t.Fatalf("got = %#v", got)
	}
	other := &source.BookSource{BookSourceURL: "https://other.example.test"}
	if got, err := runtime.EvalWithBindings(context.Background(), `source.get("token")`, Bindings{"source": other}); err != nil || got != "" {
		t.Fatalf("source.get leaked across sources: %#v %v", got, err)
	}
}

func TestRuntimeSourceStatePersistsInRepository(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := readersqlite.Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := repositories.NewSourceVariableRepository(db)
	src := &source.BookSource{BookSourceURL: "https://source.example.test"}
	first := NewRuntime(Policy{Variables: repo})
	if _, err := first.EvalWithBindings(context.Background(), `source.setVariable("v1"); source.putLoginHeader('{"Authorization":"Bearer x"}'); source.putLoginInfo("info"); source.put("k", "kv")`, Bindings{"source": src}); err != nil {
		t.Fatalf("seed error = %v", err)
	}
	// 新 Runtime 模拟进程重启：状态应从 source_variables 读回。
	second := NewRuntime(Policy{Variables: repo})
	got, err := second.EvalWithBindings(context.Background(), `[source.getVariable(), source.getLoginHeader(), source.getLoginInfo(), source.get("k"), source.variable].join("|")`, Bindings{"source": src})
	if err != nil {
		t.Fatalf("read error = %v", err)
	}
	if got != `v1|{"Authorization":"Bearer x"}|info|kv|v1` {
		t.Fatalf("got = %#v", got)
	}
	if _, err := second.EvalWithBindings(context.Background(), `source.removeLoginHeader(); source.setVariable(null)`, Bindings{"source": src}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM source_variables WHERE book_source_url = ? AND name IN (?, ?)`, src.BookSourceURL, source.VariableNameLoginHeader, source.VariableNameSourceVariable).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("removed entries still stored: %d", count)
	}
}

func TestRuntimeScriptFetchUsesEvalContext(t *testing.T) {
	canceled := make(chan error, 1)
	fetcher := fetcherFunc(func(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
		<-ctx.Done()
		canceled <- ctx.Err()
		return nil, ctx.Err()
	})
	runtime := NewRuntime(Policy{Timeout: 50 * time.Millisecond, Fetcher: fetcher})
	if _, err := runtime.Eval(context.Background(), `java.ajax("https://slow.example.test")`, nil); err == nil {
		t.Fatal("expected error")
	}
	select {
	case err := <-canceled:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("fetch ctx err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fetch context was not canceled by script timeout")
	}

	parent, cancel := context.WithCancel(context.Background())
	runtime = NewRuntime(Policy{Timeout: time.Minute, Fetcher: fetcher})
	time.AfterFunc(20*time.Millisecond, cancel)
	if _, err := runtime.Eval(parent, `java.ajax("https://slow.example.test")`, nil); err == nil {
		t.Fatal("expected error after parent cancel")
	}
	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("fetch ctx err = %v, want canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fetch context was not canceled by parent")
	}
}

func TestNamespacedCacheBounds(t *testing.T) {
	cache := newNamespacedCache(3, 64, 32)
	clock := time.Unix(0, 0)
	cache.now = func() time.Time { return clock }
	cache.Put("ns", "a", "1")
	cache.Put("ns", "b", "2")
	cache.Put("ns", "c", "3")
	cache.Put("ns", "a", "1b") // 覆盖写入移到队尾
	cache.Put("ns", "d", "4")  // 淘汰最旧的 b
	if _, ok := cache.Get("ns", "b"); ok {
		t.Fatal("oldest entry was not evicted")
	}
	if got, ok := cache.Get("ns", "a"); !ok || got != "1b" {
		t.Fatalf("a = %q %v", got, ok)
	}
	if cache.Put("ns", "big", strings.Repeat("x", 40)) {
		t.Fatal("oversized value accepted")
	}
	cache.PutTTL("ns", "ttl", "v", time.Second)
	clock = clock.Add(2 * time.Second)
	if _, ok := cache.Get("ns", "ttl"); ok {
		t.Fatal("expired entry returned")
	}
	for i := range 10 {
		cache.Put("ns", fmt.Sprintf("k%d", i), strings.Repeat("y", 20))
	}
	if cache.Len() > 3 || cache.bytes > 64 {
		t.Fatalf("len = %d bytes = %d exceed limits", cache.Len(), cache.bytes)
	}
}

func TestLogBufferBounds(t *testing.T) {
	buffer := newLogBuffer(3, 10)
	for _, message := range []string{"aa", "bb", "cc", "dd"} {
		buffer.Append(message)
	}
	if got := strings.Join(buffer.Snapshot(), ","); got != "bb,cc,dd" {
		t.Fatalf("entries = %s", got)
	}
	buffer.Append("0123456789abcdef")
	if got := buffer.Snapshot(); len(got) != 1 || got[0] != "0123456789" {
		t.Fatalf("entries = %#v", got)
	}
	runtime := NewRuntime(Policy{AllowLog: true})
	if _, err := runtime.Eval(context.Background(), `for (var i = 0; i < 5000; i++) { log("line " + i) } "ok"`, nil); err != nil {
		t.Fatal(err)
	}
	if got := runtime.Logs(); len(got) != defaultLogMaxEntries || got[len(got)-1] != "line 4999" {
		t.Fatalf("logs len = %d", len(got))
	}
}

func TestRuntimeJSLibCompiledOnceAndIsolated(t *testing.T) {
	src := &source.BookSource{BookSourceURL: "https://source.example.test", JSLib: `var counter = 0; function bump(){ counter++; return counter; }`}
	runtime := NewRuntime(Policy{})
	for range 2 {
		got, err := runtime.EvalWithBindings(context.Background(), `bump()`, Bindings{"source": src})
		if err != nil {
			t.Fatal(err)
		}
		if got != int64(1) {
			t.Fatalf("bump() = %#v, jsLib state leaked between Evals", got)
		}
	}
	if runtime.programs.len() != 1 {
		t.Fatalf("program cache len = %d, want 1", runtime.programs.len())
	}
	first, _ := runtime.programs.compile(src.JSLib)
	second, _ := runtime.programs.compile(src.JSLib)
	if first != second {
		t.Fatal("jsLib recompiled for identical content")
	}
}

func TestRuntimeJSLibInfiniteLoopUsesEvalTimeout(t *testing.T) {
	src := &source.BookSource{BookSourceURL: "https://source.example.test", JSLib: `while (true) {}`}
	runtime := NewRuntime(Policy{Timeout: 20 * time.Millisecond})
	if _, err := runtime.EvalWithBindings(context.Background(), `1`, Bindings{"source": src}); !errors.Is(err, ErrScriptTimeout) {
		t.Fatalf("error = %v, want timeout", err)
	}
}

func TestRewriteJavaCompatScript(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{"comment mention is untouched", "// JavaImporter\nfunction a(){ return 1 }\na()", "// JavaImporter\nfunction a(){ return 1 }\na()"},
		{"string mention is untouched", `var s = "with(JavaImporter)"; function b(){}`, `var s = "with(JavaImporter)"; function b(){}`},
		{"with block functions hoisted", "with (x) { function a(){ return 1 } function b(){ return 2 } }", "with (x) { a = function(){ return 1 }; b = function(){ return 2 }; }"},
		{"nested function untouched", "with (x) { var f = function(){ function inner(){} } }", "with (x) { var f = function(){ function inner(){} } }"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rewriteJavaCompatScript(tt.script); got != tt.want {
				t.Fatalf("got = %q, want %q", got, tt.want)
			}
		})
	}
	runtime := NewRuntime(Policy{})
	got, err := runtime.Eval(context.Background(), `
var javaImport = new JavaImporter();
// 中文注释 JavaImporter
with (javaImport) { function enc(s){ return "e:" + s } function dec(s){ return "d:" + s } }
enc("1") + dec("2")`, nil)
	if err != nil || got != "e:1d:2" {
		t.Fatalf("Eval() = %#v, %v", got, err)
	}
}

func TestRuntimeSingleArgJavaGetNeverFetches(t *testing.T) {
	fetcher := &recordingFetcher{}
	runtime := NewRuntime(Policy{SourceKey: "src", Fetcher: fetcher})
	got, err := runtime.Eval(context.Background(), `java.put("https://cached.example.test/key", "v"); java.get("https://cached.example.test/key") + "|" + java.get("https://other.example.test/x")`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v|" || fetcher.count() != 0 {
		t.Fatalf("got = %#v fetches = %d", got, fetcher.count())
	}
}

func TestRuntimeDoesNotExposeRawSource(t *testing.T) {
	src := &source.BookSource{BookSourceURL: "https://source.example.test", JSLib: "var x = 1;"}
	runtime := NewRuntime(Policy{})
	got, err := runtime.EvalWithBindings(context.Background(), `typeof __source + "|" + typeof source.JSLib + "|" + source.bookSourceUrl`, Bindings{"source": src})
	if err != nil {
		t.Fatal(err)
	}
	if got != "undefined|undefined|https://source.example.test" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeDefaultTimeoutWithoutDeadline(t *testing.T) {
	previous := defaultScriptTimeout
	defaultScriptTimeout = 20 * time.Millisecond
	t.Cleanup(func() { defaultScriptTimeout = previous })
	runtime := NewRuntime(Policy{})
	if _, err := runtime.Eval(context.Background(), `while (true) {}`, nil); !errors.Is(err, ErrScriptTimeout) {
		t.Fatalf("error = %v, want timeout", err)
	}
}

// 非 JSON variable 对齐 Legado BaseBook.variableMap：解析失败视为空 map，按 key 取值为空，写入时以 JSON 覆盖。
func TestCompatVariableHelpers(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		key       string
		wantGet   string
		wantFound bool
	}{
		{"json string value", `{"a":"1"}`, "a", "1", true},
		{"missing key", `{"a":"1"}`, "b", "", false},
		{"plain text variable", "not-json", "a", "", false},
		{"empty variable", "", "a", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := tt.raw
			if got := compatGetVariable(&raw, tt.key); got != tt.wantGet {
				t.Fatalf("compatGetVariable = %q, want %q", got, tt.wantGet)
			}
			if _, found := getVariableFromStruct(struct{ Variable string }{tt.raw}, tt.key); found != tt.wantFound {
				t.Fatalf("getVariableFromStruct found = %v", found)
			}
			compatPutVariable(&raw, "new", "v")
			if got := compatGetVariable(&raw, "new"); got != "v" {
				t.Fatalf("after put = %q (raw %q)", got, raw)
			}
			compatPutVariable(&raw, "new", nil)
			if got := compatGetVariable(&raw, "new"); got != "" {
				t.Fatalf("after delete = %q", got)
			}
		})
	}
}
