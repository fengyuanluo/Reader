package script

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"

	"reader/internal/domain/book"
	"reader/internal/domain/fetch"
	"reader/internal/domain/source"
	fetcherinfra "reader/internal/infra/fetcher"
)

func TestRuntimeEvalReturnsValue(t *testing.T) {
	runtime := NewRuntime(Policy{})
	got, err := runtime.Eval(context.Background(), `result.name + ":" + Math.max(1, 2)`, map[string]any{"name": "book"})
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	if got != "book:2" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeRecoversScriptError(t *testing.T) {
	runtime := NewRuntime(Policy{})
	_, err := runtime.Eval(context.Background(), `throw new Error("boom")`, nil)
	if !errors.Is(err, ErrScriptFailed) {
		t.Fatalf("error = %v, want ErrScriptFailed", err)
	}
}

func TestRuntimeRejectsUnsafeBinding(t *testing.T) {
	runtime := NewRuntime(Policy{})
	_, err := runtime.EvalWithBindings(context.Background(), `1`, Bindings{"process": map[string]any{}})
	if !errors.Is(err, ErrUnsafeGlobal) {
		t.Fatalf("error = %v, want ErrUnsafeGlobal", err)
	}
}

func TestRuntimeDoesNotExposeHostCapabilities(t *testing.T) {
	runtime := NewRuntime(Policy{})
	got, err := runtime.Eval(context.Background(), `typeof require + "," + typeof process + "," + typeof fetch`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	if got != "undefined,undefined,undefined" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeProvidesLegadoJavaHelpers(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	src := &source.BookSource{BookSourceURL: "https://source.example.test#tag"}
	got, err := runtime.EvalWithBindings(context.Background(), `
java.put("token", "abc");
[
  java.base64Encode("书"),
  java.hexDecodeToString("e4b9a6"),
  java.md5Encode("abc"),
  java.HMacHex("abc", "HmacSHA256", "key"),
  java.HMacHex("abc", "HmacMD5", "key"),
  java.desEncodeToBase64String("search", "snY%169j", "DES/ECB/PKCS5Padding", ""),
  java.encodeURI("斗破 苍穹"),
  java.encodeURI("民间故事", "gb2312"),
  java.randomUUID().length,
  java.get("token"),
  java.getString("$.version"),
  source.getVariable().split(",").length
].join("|")
`, Bindings{"result": map[string]any{"version": 1710000000}, "source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	want := "5Lmm|书|900150983cd24fb0d6963f7d28e17f72|9c196e32dc0175f86f4b1cb89289d6619de6bee699e4c378e68309ed97a1a6ab|d2fe98063f876b03193afb49b4979591|BAYBWmMcj2Y=|%E6%96%97%E7%A0%B4+%E8%8B%8D%E7%A9%B9|%C3%F1%BC%E4%B9%CA%CA%C2|36|abc|1710000000|1"
	if got != want {
		t.Fatalf("got = %#v, want %#v", got, want)
	}
}

func TestRuntimeRandomUUIDFormat(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.Eval(context.Background(), `String(java.randomUUID().valueOf())`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	uuid, ok := got.(string)
	if !ok || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(uuid) {
		t.Fatalf("uuid = %#v", got)
	}
}

func TestRuntimeRandomUUIDReplaceUsesJavaStringStyle(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.Eval(context.Background(), `java.randomUUID().toString().replace("-", "").toLowerCase()`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	uuid, ok := got.(string)
	if !ok || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(uuid) {
		t.Fatalf("uuid = %#v", got)
	}
}

func TestRuntimeJavaGetReadsBoundBookVariables(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	b := &book.Book{Variable: `{"bid":"123","headers":"{\"headers\":{\"X-Test\":\"1\"}}"}`}

	got, err := runtime.EvalWithBindings(context.Background(), `java.put("cid", "9"); [java.get("bid"), java.get("headers"), java.get("cid")].join("|")`, Bindings{"book": b})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != `123|{"headers":{"X-Test":"1"}}|9` {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeBookBindingExposesGetAndPutVariableHelpers(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	b := &book.Book{Variable: `{"custom":"story"}`}

	got, err := runtime.EvalWithBindings(context.Background(), `[book.getVariable("custom"), book.putVariable("bid", "42"), book.getVariable("bid")].join("|")`, Bindings{"book": b})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "story|42|42" {
		t.Fatalf("got = %#v", got)
	}
	if !strings.Contains(b.Variable, `"bid":"42"`) {
		t.Fatalf("variable = %q", b.Variable)
	}
}

func TestRuntimeSearchBookBindingExposesGetAndPutVariableHelpers(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	b := &book.SearchBook{Variable: `{"custom":"story"}`}

	got, err := runtime.EvalWithBindings(context.Background(), `[book.getVariable("custom"), book.putVariable("bid", "42"), book.getVariable("bid")].join("|")`, Bindings{"book": b})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "story|42|42" {
		t.Fatalf("got = %#v", got)
	}
	if !strings.Contains(b.Variable, `"bid":"42"`) {
		t.Fatalf("variable = %q", b.Variable)
	}
}

func TestRuntimeSharesJavaPutCacheAcrossRuntimeInstances(t *testing.T) {
	cache := NewNamespacedCache()
	r1 := NewRuntime(Policy{SourceKey: "src", Cache: cache})
	r2 := NewRuntime(Policy{SourceKey: "src", Cache: cache})

	if _, err := r1.Eval(context.Background(), `java.put("tmpToken", "abc123")`, nil); err != nil {
		t.Fatalf("r1 Eval() error = %v", err)
	}
	got, err := r2.Eval(context.Background(), `java.get("tmpToken")`, nil)
	if err != nil {
		t.Fatalf("r2 Eval() error = %v", err)
	}
	if got != "abc123" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeJavaPutUsesEmptyNamespaceWhenSourceKeyMissing(t *testing.T) {
	cache := NewNamespacedCache()
	r1 := NewRuntime(Policy{Cache: cache})
	r2 := NewRuntime(Policy{Cache: cache})

	if _, err := r1.Eval(context.Background(), `java.put("tmpToken", "abc123")`, nil); err != nil {
		t.Fatalf("r1 Eval() error = %v", err)
	}
	got, err := r2.Eval(context.Background(), `java.get("tmpToken")`, nil)
	if err != nil {
		t.Fatalf("r2 Eval() error = %v", err)
	}
	if got != "abc123" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeDerivesSourceKeyFromSourceBindingsWhenMissing(t *testing.T) {
	cache := NewNamespacedCache()
	src := &source.BookSource{BookSourceURL: "https://source.example.test/root"}
	r1 := NewRuntime(Policy{Cache: cache})
	r2 := NewRuntime(Policy{Cache: cache})

	if _, err := r1.EvalWithBindings(context.Background(), `java.put("tmpToken", "abc123")`, Bindings{"source": SourceBindings(src), "__source": src}); err != nil {
		t.Fatalf("r1 EvalWithBindings() error = %v", err)
	}
	got, err := r2.EvalWithBindings(context.Background(), `java.get("tmpToken")`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("r2 EvalWithBindings() error = %v", err)
	}
	if got != "abc123" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeSharedInstanceKeepsSourceNamespacesPerEval(t *testing.T) {
	cache := NewNamespacedCache()
	runtime := NewRuntime(Policy{Cache: cache})
	srcA := &source.BookSource{BookSourceURL: "https://source-a.example.test/root"}
	srcB := &source.BookSource{BookSourceURL: "https://source-b.example.test/root"}

	if _, err := runtime.EvalWithBindings(context.Background(), `java.put("tmpToken", "a123")`, Bindings{"source": SourceBindings(srcA), "__source": srcA}); err != nil {
		t.Fatalf("EvalWithBindings(source-a) error = %v", err)
	}
	if _, err := runtime.EvalWithBindings(context.Background(), `java.put("tmpToken", "b456")`, Bindings{"source": SourceBindings(srcB), "__source": srcB}); err != nil {
		t.Fatalf("EvalWithBindings(source-b) error = %v", err)
	}

	gotA, err := runtime.EvalWithBindings(context.Background(), `java.get("tmpToken")`, Bindings{"source": SourceBindings(srcA), "__source": srcA})
	if err != nil {
		t.Fatalf("EvalWithBindings(get source-a) error = %v", err)
	}
	gotB, err := runtime.EvalWithBindings(context.Background(), `java.get("tmpToken")`, Bindings{"source": SourceBindings(srcB), "__source": srcB})
	if err != nil {
		t.Fatalf("EvalWithBindings(get source-b) error = %v", err)
	}
	if gotA != "a123" || gotB != "b456" {
		t.Fatalf("gotA=%#v gotB=%#v", gotA, gotB)
	}
}

func TestRuntimeSourceBindingsExposeBookSourceCommentAndVariableComment(t *testing.T) {
	runtime := NewRuntime(Policy{})
	src := &source.BookSource{
		BookSourceURL:     "https://source.example.test/root",
		BookSourceName:    "Source",
		BookSourceGroup:   "Group",
		BookSourceType:    7,
		BookSourceComment: "function run(){ return 1 }",
		VariableComment:   "token comment",
	}
	got, err := runtime.EvalWithBindings(context.Background(), `
[source.bookSourceName, source.bookSourceGroup, source.bookSourceType, source.bookSourceComment, source.variableComment].join("|")
`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	want := "Source|Group|7|function run(){ return 1 }|token comment"
	if got != want {
		t.Fatalf("got = %#v, want %#v", got, want)
	}
}

func TestRuntimeProvidesWebViewUserAgentHelper(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.EvalWithBindings(context.Background(), `java.getWebViewUA()`, Bindings{})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != defaultWebViewUserAgent {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeProvidesTraditionalSimplifiedChineseHelpers(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.EvalWithBindings(context.Background(), `[java.t2s("輕小說"), java.s2t("轻小说")].join("|")`, Bindings{})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "轻小说|輕小說" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeStringResultSupportsLegadoResponseHelpers(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.EvalWithBindings(context.Background(), `[
  result.body(),
  result.url(),
  result.code(),
  result.header("X-Trace"),
  result.headers().get("X-Trace")
].join("|")`, Bindings{"result": "payload"})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "payload||200||" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeProvidesLoginInfoAndHeaderHelpers(t *testing.T) {
	runtime := NewRuntime(Policy{})
	src := &source.BookSource{BookSourceURL: "https://fanqie.example.test/root"}
	got, err := runtime.EvalWithBindings(context.Background(), `
source.putLoginHeader('{"Authorization":"Bearer ok"}');
source.putLoginInfo('{"手机号":"13800138000","手动登录Token":"sid"}');
[
  source.getLoginInfo(),
  source.getLoginHeaderMap().get("Authorization"),
  source.getLoginInfoMap().get("手机号"),
  source.getLoginInfoMap()["手动登录Token"]
].join("|")
`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	want := `{"手机号":"13800138000","手动登录Token":"sid"}|Bearer ok|13800138000|sid`
	if got != want {
		t.Fatalf("got = %#v, want %#v", got, want)
	}
}

func TestRuntimeProvidesEmptyLoginInfoMapWhenUnset(t *testing.T) {
	runtime := NewRuntime(Policy{})
	src := &source.BookSource{BookSourceURL: "https://fanqie.example.test/empty"}
	got, err := runtime.EvalWithBindings(context.Background(), `[source.getLoginInfoMap().get("token"), source.getLoginInfoMap()["token"], source.getLoginHeaderMap().get("Authorization")].join("|")`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "||" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeProvidesLegadoCookieStoreBinding(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.Eval(context.Background(), `
cookie.setCookie("https://www.example.test/book", "sid=abc; theme=dark");
cookie.replaceCookie("https://example.test/next", "sid=def; token=xyz");
[
  cookie.getCookie("https://example.test/"),
  cookie.getCookie("https://example.test/", "sid"),
  cookie.getKey("https://www.example.test/", "token"),
  cookie.mapToCookie(cookie.cookieToMap("b=2; a=1"))
].join("|")
`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	want := "sid=def; theme=dark; token=xyz|def|xyz|a=1; b=2"
	if got != want {
		t.Fatalf("got = %#v, want %#v", got, want)
	}
}

func TestRuntimeCookieBindingsShareSessionStore(t *testing.T) {
	session := fetcherinfra.NewSessionCookieStore()
	runtime := NewRuntime(Policy{SourceKey: "src", Cookie: session})
	got, err := runtime.Eval(context.Background(), `
cookie.setCookie("https://www.example.test/book", "sid=abc");
cookie.replaceCookie("https://example.test/next", "token=xyz");
cookie.getCookie("https://example.test/")
`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	if got != "sid=abc; token=xyz" && got != "token=xyz; sid=abc" {
		t.Fatalf("got = %#v", got)
	}
	sessionCookies := session.List("example.test")
	if sessionCookies["sid"] != "abc" || sessionCookies["token"] != "xyz" {
		t.Fatalf("session cookies = %#v", sessionCookies)
	}
}

func TestRuntimeJavaAjaxSharesCookiesWithCookieBinding(t *testing.T) {
	session := fetcherinfra.NewSessionCookieStore()
	manager := fetcherinfra.NewCookieManager(nil, session)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/seed" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "seeded"})
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	serverURL := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)

	f := fetcherinfra.NewHTTPFetcher(fetcherinfra.WithAllowPrivateNetwork(true), fetcherinfra.WithCookieManager(manager))
	src := &source.BookSource{BookSourceURL: "https://source.example.test/root", EnabledCookieJar: true}
	runtime := NewRuntime(Policy{SourceKey: "src", Fetcher: f, Cookie: session, Source: src})
	got, err := runtime.Eval(context.Background(), `
	java.ajax("`+serverURL+`/seed");
	cookie.getCookie("`+serverURL+`/next");
	`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	t.Logf("session = %#v", session.List("localhost"))
	if got != "SID=seeded" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeProvidesLegadoAESBase64DecodeHelper(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.Eval(context.Background(), `
java.aesBase64DecodeToString(
  "CrmyNAzlhtf+PS2Axon2/ewjcihKE7gvQni28oKn3Vs=",
  "f041c49714d39908",
  "AES/CBC/PKCS5Padding",
  "0123456789abcdef"
)
`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	want := "https://example.test/chapter/1"
	if got != want {
		t.Fatalf("got = %#v, want %#v", got, want)
	}
}

func TestRuntimeProvidesCreateSymmetricCryptoCompat(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.Eval(context.Background(), `
var cipher = java.createSymmetricCrypto(
  "AES/CBC/PKCS5Padding",
  "f041c49714d39908",
  "0123456789abcdef"
);
var encoded = cipher.encryptBase64("https://example.test/chapter/1");
[cipher.decryptStr(encoded), encoded].join("|")
`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	want := "https://example.test/chapter/1|CrmyNAzlhtf+PS2Axon2/ewjcihKE7gvQni28oKn3Vs="
	if got != want {
		t.Fatalf("got = %#v, want %#v", got, want)
	}
}

func TestRuntimeProvidesRestrictedJavaImporterCryptoCompat(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.Eval(context.Background(), `
var javaImport = new JavaImporter();
javaImport.importPackage(Packages.java.lang, Packages.javax.crypto.spec, Packages.javax.crypto, Packages.java.util);
with(javaImport) {
  function decode(content) {
    var ivEncData = Base64.getDecoder().decode(String(content));
    var key = SecretKeySpec(String("f041c49714d39908").getBytes(), "AES");
    var iv = IvParameterSpec(Arrays.copyOfRange(ivEncData, 0, 16));
    var chipher = Cipher.getInstance("AES/CBC/PKCS5Padding");
    chipher.init(2, key, iv);
    return String(chipher.doFinal(Arrays.copyOfRange(ivEncData, 16, ivEncData.length)));
  }
}
decode("MDEyMzQ1Njc4OWFiY2RlZgq5sjQM5YbX/j0tgMaJ9v3sI3IoShO4L0J4tvKCp91b")
`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	want := "https://example.test/chapter/1"
	if got != want {
		t.Fatalf("got = %#v, want %#v", got, want)
	}
}

func TestRuntimeProvidesRestrictedJavaImporterGzipCompat(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.Eval(context.Background(), `
var javaImport = new JavaImporter();
javaImport.importPackage(Packages.java.lang, Packages.java.io, Packages.java.util, Packages.java.util.zip);
with(javaImport) {
  function decode(content) {
    decode = Base64.getDecoder().decode(String(content));
    byteArrayOutputStream = new ByteArrayOutputStream();
    byteArrayInputStream = new ByteArrayInputStream(decode);
    gZIPInputStream = new GZIPInputStream(byteArrayInputStream);
    bArr = String(" ").getBytes();
    while (true) {
      read = gZIPInputStream.read(bArr);
      if (read > 0) {
        byteArrayOutputStream.write(bArr, 0, read);
      } else {
        gZIPInputStream.close();
        byteArrayInputStream.close();
        byteArrayOutputStream.close();
        return byteArrayOutputStream.toString();
      }
    }
  }
}
decode("H4sIAGg992kC/3u+Zs2THQ3P1m0FAFllbKMJAAAA")
`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	if got != "第一段" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeProvidesRestrictedHutoolAndSourceCompat(t *testing.T) {
	src := &source.BookSource{BookSourceURL: "https://reading.snssdk.com#lcs"}
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.EvalWithBindings(context.Background(), `
var javaImport = new JavaImporter();
javaImport.importPackage(
  Packages.okhttp3,
  Packages.cn.hutool.core.util,
  Packages.cn.hutool.core.codec,
  Packages.cn.hutool.crypto.digest
);
DigestUtil.md5Hex("abc") + "|" + StrUtil.reverse("abc") + "|" + source.getLoginHeader()
`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "900150983cd24fb0d6963f7d28e17f72|cba|" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeLoadsSourceJSLibBeforeEval(t *testing.T) {
	src := &source.BookSource{
		BookSourceURL: "https://source.example.test##tag",
		JSLib:         `function sign(key){ return source.getKey() + ":" + key; }`,
	}
	runtime := NewRuntime(Policy{SourceKey: "src"})

	got, err := runtime.EvalWithBindings(context.Background(), `sign(key)`, Bindings{
		"key":      "book",
		"source":   SourceBindings(src),
		"__source": src,
	})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "https://source.example.test:book" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeRewritesRhinoLetConditionInSourceJSLib(t *testing.T) {
	src := &source.BookSource{
		BookSourceURL: "https://fanqie.hnxianxin.cn/＃妍希",
		JSLib: `function getBookId(url, page) {
  let list
  let arr
  let $ = JSON.parse(url).data
  if ($.book_shelf_info != 0 && $.book_shelf_info != undefined) {
    arr = $.book_shelf_info.map($ => $.book_id)
  } else if (let (list = $.data_list) list != 0 && $.data_list != undefined)  {
    arr = $.data_list.map($ => $.book_id_str)
  } else {
    arr = []
  }
  return arr.slice(page*100, (page+1)*100)
}`,
	}
	runtime := NewRuntime(Policy{SourceKey: "src"})

	got, err := runtime.EvalWithBindings(context.Background(), `getBookId('{"data":{"data_list":[{"book_id_str":"7268620849496722495"}]}}', 0)[0]`, Bindings{
		"source":   SourceBindings(src),
		"__source": src,
	})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "7268620849496722495" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeLoadsJavaImporterFunctionsFromSourceJSLib(t *testing.T) {
	src := &source.BookSource{
		BookSourceURL: "https://source.example.test##tag",
		JSLib: `var javaImport = new JavaImporter();
javaImport.importPackage(Packages.java.lang, Packages.cn.hutool.core.util, Packages.cn.hutool.crypto.digest);
with(javaImport) {
  function xGorgon(input) {
    return DigestUtil.md5Hex(StrUtil.reverse(String(input)));
  }
}`,
	}
	runtime := NewRuntime(Policy{SourceKey: "src"})

	got, err := runtime.EvalWithBindings(context.Background(), `xGorgon("abc")`, Bindings{
		"source":   SourceBindings(src),
		"__source": src,
	})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "3944b025c9ca7eec3154b44666ae04a0" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeProvidesSourceCompatStateAndLoginURLFields(t *testing.T) {
	src := &source.BookSource{
		BookSourceURL: "https://qcbook.taoyuewenhua.net",
		LoginURL:      `function login(){ source.setVariable("token=ok"); source.putLoginHeader("{\"Authorization\":\"Bearer ok\"}"); }`,
	}
	runtime := NewRuntime(Policy{SourceKey: src.BookSourceURL})

	got, err := runtime.EvalWithBindings(context.Background(), `
eval(String(source.loginUrl));
login();
[source.getVariable(), source.getLoginHeader()].join("|")
`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != `token=ok|{"Authorization":"Bearer ok"}` {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeProvidesJavaUtilUUIDAndAndroidBuildCompat(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.Eval(context.Background(), `
var aid = String(Packages.java.util.UUID.randomUUID()).replace(/-/g, "").slice(0, 16);
var did = String(Packages.java.util.UUID.randomUUID());
var model = Packages.android.os.Build.MODEL;
var brand = Packages.android.os.Build.MANUFACTURER;
[aid.length, did.length, model, brand].join("|")
`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	if got != "16|36|Reader|Reader" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeProvidesPackagesUUIDDirectAccess(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.Eval(context.Background(), `String(Packages.java.util.UUID.randomUUID())`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	if s, ok := got.(string); !ok || len(s) != 36 || s[14] != '4' {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeProvidesJavaGetElementsCompat(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.EvalWithBindings(context.Background(), `
var data=[];
list=java.getElements('.g');
c=java.getElements('.content');
for(i=0;i<list.length;i++){
  text=list[i].select('a').text();
  href=list[i].select('a').attr('href');
  desc=c[i].text();
  data.push(text + "|" + href + "|" + desc);
}
data.join("\n")
`, Bindings{"result": `<div class="g"><a href="/story/1">故事一</a></div><div class="content">简介一</div><div class="g"><a href="/story/2">故事二</a></div><div class="content">简介二</div>`})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	want := "故事一|/story/1|简介一\n故事二|/story/2|简介二"
	if got != want {
		t.Fatalf("got = %#v, want %#v", got, want)
	}
}

func TestRuntimeJavaConnectUsesSourceHeadersAndURLOptions(t *testing.T) {
	calls := 0
	fetcher := fetcherFunc(func(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
		calls++
		if req.URL != "https://source.example.test/api" {
			t.Fatalf("url = %q", req.URL)
		}
		if calls == 1 {
			if req.Method != "POST" {
				t.Fatalf("method = %q", req.Method)
			}
			if string(req.Body) != "q=book" {
				t.Fatalf("body = %q", string(req.Body))
			}
			if req.Headers["User-Agent"] != "Reader-Test" || req.Headers["X-Rule"] != "ok" {
				t.Fatalf("headers = %#v", req.Headers)
			}
		} else {
			if req.Headers["User-Agent"] != "Reader-Test" {
				t.Fatalf("headers = %#v", req.Headers)
			}
		}
		return &fetch.Response{StatusCode: 200, Body: []byte("body"), URL: req.URL}, nil
	})
	src := &source.BookSource{
		BookSourceURL: "https://source.example.test/root",
		Header:        `{'User-Agent':'Reader-Test'}`,
	}
	runtime := NewRuntime(Policy{BaseURL: src.BookSourceURL, SourceKey: src.BookSourceURL, Fetcher: fetcher})

	got, err := runtime.EvalWithBindings(context.Background(), `
var res = java.connect("/api,{\"method\":\"POST\",\"body\":\"q=book\",\"headers\":{\"X-Rule\":\"ok\"}}");
res.body() + "|" + res.body + "|" + res.url() + "|" + res.url + "|" + java.ajax("/api")
`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if !strings.HasPrefix(got.(string), "body|body|https://source.example.test/api|https://source.example.test/api|") {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeLegadoResponseSupportsRawAndHeaderChains(t *testing.T) {
	fetcher := fetcherFunc(func(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
		if req.Method == "POST" {
			if string(req.Body) != "searchkey=book" {
				t.Fatalf("post body = %q", string(req.Body))
			}
			return &fetch.Response{StatusCode: 302, Headers: http.Header{"Location": []string{"https://source.example.test/result.html"}}, Body: []byte("redirect"), URL: "https://source.example.test/search.html"}, nil
		}
		return &fetch.Response{StatusCode: 200, Headers: http.Header{"X-Trace": []string{"trace"}}, Body: []byte("home"), URL: "https://source.example.test/"}, nil
	})
	src := &source.BookSource{BookSourceURL: "https://source.example.test"}
	runtime := NewRuntime(Policy{SourceKey: src.BookSourceURL, Fetcher: fetcher})

	got, err := runtime.EvalWithBindings(context.Background(), `
[
  java.connect(source.getKey()).raw().request().url(),
  java.connect(source.getKey()).url(),
  java.connect(source.getKey()).code(),
  java.connect(source.getKey()).body(),
  java.connect(source.getKey()).body,
  java.connect(source.getKey()).header("X-Trace"),
  java.post("/search.html", "searchkey=book", {"X-Rule":"ok"}).header("Location")
].join("|")
`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	want := "https://source.example.test/|https://source.example.test/|200|home|home|trace|https://source.example.test/result.html"
	if got != want {
		t.Fatalf("got = %#v, want %#v", got, want)
	}
}

func TestRuntimeJavaAjaxAppliesFormContentTypeForStringBody(t *testing.T) {
	fetcher := fetcherFunc(func(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
		if req.Method != "POST" {
			t.Fatalf("method = %q", req.Method)
		}
		if string(req.Body) != "chapter_id=1&bookId=218440" {
			t.Fatalf("body = %q", string(req.Body))
		}
		if req.Headers["Content-Type"] != "application/x-www-form-urlencoded" {
			t.Fatalf("headers = %#v", req.Headers)
		}
		return &fetch.Response{StatusCode: 200, Body: []byte(`ok`), URL: req.URL}, nil
	})
	src := &source.BookSource{BookSourceURL: "https://source.example.test"}
	runtime := NewRuntime(Policy{SourceKey: src.BookSourceURL, Fetcher: fetcher})

	got, err := runtime.EvalWithBindings(context.Background(), `
java.ajax("https://api.example.test/content,{\"method\":\"POST\",\"body\":\"chapter_id=1&bookId=218440\"}")
`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != `ok` {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeJavaAjaxEncodesQueryByDefault(t *testing.T) {
	fetcher := fetcherFunc(func(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
		if req.URL != "https://api.example.test/search?keyword=%E8%AE%BA%E8%AF%AD" {
			t.Fatalf("url = %q", req.URL)
		}
		return &fetch.Response{StatusCode: 200, Body: []byte(`ok`), URL: req.URL}, nil
	})
	src := &source.BookSource{BookSourceURL: "https://source.example.test"}
	runtime := NewRuntime(Policy{SourceKey: src.BookSourceURL, Fetcher: fetcher})

	got, err := runtime.EvalWithBindings(context.Background(), `
java.ajax("https://api.example.test/search?keyword=论语")
`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != `ok` {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeJavaAjaxAppliesHeaderJSFromSource(t *testing.T) {
	fetcher := fetcherFunc(func(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
		if req.Headers["X-Token"] != "abc" {
			t.Fatalf("headers = %#v", req.Headers)
		}
		return &fetch.Response{StatusCode: 200, Body: []byte(`ok`), URL: req.URL}, nil
	})
	src := &source.BookSource{
		BookSourceURL: "https://source.example.test",
		Header:        `@js:JSON.stringify({"X-Token":"abc"})`,
	}
	runtime := NewRuntime(Policy{SourceKey: src.BookSourceURL, BaseURL: src.BookSourceURL, Source: src, Fetcher: fetcher})

	got, err := runtime.EvalWithBindings(context.Background(), `
java.ajax("https://api.example.test/search?keyword=论语")
`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != `ok` {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeJavaAjaxRejectsUnsupportedURLOption(t *testing.T) {
	src := &source.BookSource{BookSourceURL: "https://source.example.test"}
	runtime := NewRuntime(Policy{SourceKey: src.BookSourceURL, BaseURL: src.BookSourceURL, Source: src, Fetcher: fetcherFunc(func(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
		t.Fatalf("fetch should not be called when option is unsupported")
		return nil, nil
	})})

	got, err := runtime.EvalWithBindings(context.Background(), `
java.ajax("https://api.example.test/search,{\"webView\":true}")
`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(got.(string), "unsupported url option") {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeJavaAjaxAppliesRetryOption(t *testing.T) {
	fetcher := fetcherFunc(func(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
		if req.Retry != 2 {
			t.Fatalf("req = %#v", req)
		}
		return &fetch.Response{StatusCode: 200, Body: []byte(`ok`), URL: req.URL}, nil
	})
	src := &source.BookSource{BookSourceURL: "https://source.example.test"}
	runtime := NewRuntime(Policy{SourceKey: src.BookSourceURL, BaseURL: src.BookSourceURL, Source: src, Fetcher: fetcher})

	got, err := runtime.EvalWithBindings(context.Background(), `
java.ajax("https://api.example.test/search,{\"retry\":2}")
`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != `ok` {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeSelectionBindingSupportsParentNodeTraversal(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "https://source.example.test"})
	got, err := runtime.EvalWithBindings(context.Background(), `
var node = java.getElements('.chapter')[0];
node.parentNode().parentNode().select('h2').text() + '：' + node.text()
`, Bindings{"result": `<div class="section"><h2>第一卷</h2><div class="chapter-list"><div class="chapter"><a href="/c1">序章</a></div></div></div>`})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "第一卷：序章" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeWrapsSelectionInResultBinding(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`<div class="section"><h2>第一卷</h2><div class="chapter-list"><div class="chapter"><a href="/c1">序章</a></div></div></div>`))
	if err != nil {
		t.Fatalf("NewDocumentFromReader() error = %v", err)
	}
	runtime := NewRuntime(Policy{SourceKey: "https://source.example.test"})
	got, err := runtime.EvalWithBindings(context.Background(), `
result.parentNode().parentNode().select('h2').text() + '：' + result.text()
`, Bindings{"result": doc.Find(".chapter").First()})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "第一卷：序章" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeJavaAjaxExpandsInlineTemplatesInCustomURLBody(t *testing.T) {
	fetcher := fetcherFunc(func(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
		if req.Method != "POST" {
			t.Fatalf("method = %q", req.Method)
		}
		body := string(req.Body)
		if strings.Contains(body, "{{") || strings.Contains(body, "}}") {
			t.Fatalf("body still contains templates: %q", body)
		}
		if body != "bookId=229221&chapterId=229222" {
			t.Fatalf("body = %q", body)
		}
		return &fetch.Response{StatusCode: 200, Body: []byte(`ok`), URL: req.URL}, nil
	})
	src := &source.BookSource{BookSourceURL: "https://source.example.test"}
	runtime := NewRuntime(Policy{SourceKey: src.BookSourceURL, Fetcher: fetcher})
	b := &book.Book{Variable: `{"id":"229221"}`}
	ch := &book.BookChapter{Variable: `{"chapterId":"229222"}`}

	got, err := runtime.EvalWithBindings(context.Background(), `
java.ajax("https://api.example.test/content,{\"method\":\"POST\",\"body\":\"bookId={{java.get('id')}}&chapterId={{java.get('chapterId')}}\"}")
`, Bindings{"source": SourceBindings(src), "__source": src, "book": b, "chapter": ch})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != `ok` {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeJavaAjaxAllReturnsResponseArray(t *testing.T) {
	fetcher := fetcherFunc(func(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
		return &fetch.Response{StatusCode: 200, Body: []byte(`body:` + req.URL), URL: req.URL}, nil
	})
	src := &source.BookSource{BookSourceURL: "https://source.example.test"}
	runtime := NewRuntime(Policy{SourceKey: src.BookSourceURL, Fetcher: fetcher})

	got, err := runtime.EvalWithBindings(context.Background(), `
var res = java.ajaxAll(["/a", "/b"]);
res[0].code() + "|" + res[0].body() + "|" + res[1].body()
`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	want := "200|body:https://source.example.test/a|body:https://source.example.test/b"
	if got != want {
		t.Fatalf("got = %#v, want %#v", got, want)
	}
}

func TestRuntimeLegadoResponseHeadersCallable(t *testing.T) {
	fetcher := fetcherFunc(func(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
		return &fetch.Response{
			StatusCode: 200,
			Headers:    http.Header{"Set-Cookie": []string{"sid=abc"}, "X-Trace": []string{"trace"}},
			Body:       []byte(`ok`),
			URL:        req.URL,
		}, nil
	})
	src := &source.BookSource{BookSourceURL: "https://source.example.test"}
	runtime := NewRuntime(Policy{SourceKey: src.BookSourceURL, Fetcher: fetcher})

	got, err := runtime.EvalWithBindings(context.Background(), `
var res = java.get("https://source.example.test");
res.headers().get("Set-Cookie") + "|" + res.headers().get("x-trace")
`, Bindings{"source": SourceBindings(src), "__source": src})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "sid=abc|trace" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeProvidesJavaTimeFormatUTCForTimestamp(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.Eval(context.Background(), `java.timeFormatUTC(1710000000000, "yyyy-MM-dd", 8)`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	if got != "2024-03-10" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeProvidesJavaTimeFormatUTCForDateObject(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.Eval(context.Background(), `java.timeFormatUTC(new Date("2024-03-10T00:00:00Z"), "yyyyMMdd", 8)`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	if got != "20240310" && got != "20240311" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRuntimeJavaGetStringFallsBackToSrcContent(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	got, err := runtime.EvalWithBindings(context.Background(), `
java.getString("$.data.freeContUrlPrefix") + "|" + java.getString("$.contUrlSuffix")
`, Bindings{
		"result": map[string]any{"contUrlSuffix": "?chapterId=1"},
		"src":    map[string]any{"data": map[string]any{"freeContUrlPrefix": "http://c13.shuqireader.com/qswebapi/chapter/contentfree/"}},
	})
	if err != nil {
		t.Fatalf("EvalWithBindings() error = %v", err)
	}
	if got != "http://c13.shuqireader.com/qswebapi/chapter/contentfree/|?chapterId=1" {
		t.Fatalf("got = %#v", got)
	}
}

type fetcherFunc func(context.Context, fetch.Request) (*fetch.Response, error)

func (f fetcherFunc) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	return f(ctx, req)
}
