package script

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dop251/goja"
	"github.com/longbridgeapp/opencc"
	"golang.org/x/net/publicsuffix"

	domainbook "reader/internal/domain/book"
	"reader/internal/domain/fetch"
	domainsource "reader/internal/domain/source"
	fetcherinfra "reader/internal/infra/fetcher"
	"reader/internal/infra/logging"
	ruleinfra "reader/internal/infra/rule"
	urlrule "reader/internal/infra/rule/url"
)

var (
	ErrScriptFailed  = errors.New("script_failed")
	ErrScriptTimeout = errors.New("script_timeout")
	ErrUnsafeGlobal  = errors.New("unsafe global access")
)

const defaultWebViewUserAgent = "Mozilla/5.0 (Linux; Android 10; K; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/122.0.0.0 Mobile Safari/537.36"

var (
	openccT2SOnce sync.Once
	openccT2S     *opencc.OpenCC
	openccT2SErr  error
	openccS2TOnce sync.Once
	openccS2T     *opencc.OpenCC
	openccS2TErr  error
)

type Bindings map[string]any

type Policy struct {
	AllowLog  bool
	Timeout   time.Duration
	Stage     string
	SourceKey string
	BaseURL   string
	Fetcher   fetch.Fetcher
	Source    *domainsource.BookSource
	Cookie    *fetcherinfra.SessionCookieStore
	Cache     *NamespacedCache
}

type Runtime struct {
	policy Policy
	cache  *NamespacedCache
	cookie *MemoryCookieStore
	logs   []string
	mu     sync.Mutex
}

type evalContext struct {
	sourceKey string
}

func NewRuntime(policy Policy) *Runtime {
	return &Runtime{policy: policy, cache: policy.Cache}
}

func (r *Runtime) Eval(ctx context.Context, script string, input any) (out any, err error) {
	return r.EvalWithBindings(ctx, script, Bindings{"result": input})
}

func (r *Runtime) EvalWithBindings(ctx context.Context, script string, bindings Bindings) (out any, err error) {
	vm := goja.New()
	src := sourceFromBindings(r.policy.Source, bindings)
	runCtx := evalContext{sourceKey: r.sourceKeyForBindings(src, bindings)}
	if err := r.installSafeGlobals(vm, src, runCtx); err != nil {
		return nil, err
	}
	for key, value := range bindings {
		if isForbiddenBinding(key) {
			return nil, fmt.Errorf("%w: %s", ErrUnsafeGlobal, key)
		}
		if err := vm.Set(key, prepareBindingValue(vm, key, value)); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrScriptFailed, err)
		}
	}
	if err := r.loadSourceJSLib(ctx, vm, src); err != nil {
		return nil, err
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if errors.Is(fmt.Errorf("%v", recovered), ErrScriptTimeout) {
				err = r.wrapError(ErrScriptTimeout, recovered)
			} else {
				err = r.wrapError(ErrScriptFailed, recovered)
			}
			out = nil
		}
	}()
	cancelInterrupt := r.installInterrupt(ctx, vm)
	defer cancelInterrupt()
	value, err := vm.RunString(rewriteJavaCompatScript(script))
	if err != nil {
		if strings.Contains(err.Error(), ErrScriptTimeout.Error()) {
			return nil, r.wrapError(ErrScriptTimeout, err)
		}
		return nil, r.wrapError(ErrScriptFailed, err)
	}
	return value.Export(), nil
}

func (r *Runtime) sourceKeyForBindings(src *domainsource.BookSource, bindings Bindings) string {
	if key := strings.TrimSpace(r.policy.SourceKey); key != "" {
		return stripSourceSuffix(key)
	}
	return stripSourceSuffix(sourceKeyFromBindings(src, bindings))
}

func sourceFromBindings(fallback *domainsource.BookSource, bindings Bindings) *domainsource.BookSource {
	src := fallback
	if src == nil {
		if raw, ok := bindings["__source"].(*domainsource.BookSource); ok {
			src = raw
		}
	}
	if src == nil {
		if raw, ok := bindings["source"].(*domainsource.BookSource); ok {
			src = raw
		}
	}
	return src
}

func prepareBindingValue(vm *goja.Runtime, key string, value any) any {
	if sel, ok := value.(*goquery.Selection); ok && sel != nil {
		return newSelectionBinding(sel)
	}
	switch key {
	case "book":
		if b, ok := value.(*domainbook.Book); ok && b != nil {
			return map[string]any{
				"name":               b.Name,
				"author":             b.Author,
				"bookUrl":            b.BookURL,
				"tocUrl":             b.TocURL,
				"origin":             b.Origin,
				"originName":         b.OriginName,
				"kind":               b.Kind,
				"coverUrl":           b.CoverURL,
				"intro":              b.Intro,
				"type":               b.Type,
				"wordCount":          b.WordCount,
				"latestChapterTitle": b.LatestChapterTitle,
				"originOrder":        b.OriginOrder,
				"variable":           b.Variable,
				"getVariable":        func(name string) string { return compatGetVariable(&b.Variable, name) },
				"putVariable":        func(name string, raw any) string { return compatPutVariable(&b.Variable, name, raw) },
			}
		}
		if b, ok := value.(*domainbook.SearchBook); ok && b != nil {
			return map[string]any{
				"name":               b.Name,
				"author":             b.Author,
				"bookUrl":            b.BookURL,
				"tocUrl":             b.TocURL,
				"origin":             b.Origin,
				"originName":         b.OriginName,
				"kind":               b.Kind,
				"coverUrl":           b.CoverURL,
				"intro":              b.Intro,
				"type":               b.Type,
				"wordCount":          b.WordCount,
				"latestChapterTitle": b.LatestChapterTitle,
				"originOrder":        b.OriginOrder,
				"variable":           b.Variable,
				"getVariable":        func(name string) string { return compatGetVariable(&b.Variable, name) },
				"putVariable":        func(name string, raw any) string { return compatPutVariable(&b.Variable, name, raw) },
			}
		}
	case "chapter":
		if c, ok := value.(*domainbook.BookChapter); ok && c != nil {
			return map[string]any{
				"url":         c.URL,
				"title":       c.Title,
				"isVolume":    c.IsVolume,
				"baseUrl":     c.BaseURL,
				"bookUrl":     c.BookURL,
				"index":       c.Index,
				"isVip":       c.IsVIP,
				"isPay":       c.IsPay,
				"resourceUrl": c.ResourceURL,
				"tag":         c.Tag,
				"wordCount":   c.WordCount,
				"variable":    c.Variable,
				"getVariable": func(name string) string { return compatGetVariable(&c.Variable, name) },
				"putVariable": func(name string, raw any) string { return compatPutVariable(&c.Variable, name, raw) },
			}
		}
	}
	return value
}

func sourceKeyFromBindings(src *domainsource.BookSource, bindings Bindings) string {
	if src != nil && strings.TrimSpace(src.BookSourceURL) != "" {
		return src.BookSourceURL
	}
	if raw, ok := bindings["source"].(map[string]any); ok {
		if value, ok := raw["bookSourceUrl"]; ok && strings.TrimSpace(fmt.Sprint(value)) != "" {
			return fmt.Sprint(value)
		}
		if value, ok := raw["key"]; ok && strings.TrimSpace(fmt.Sprint(value)) != "" {
			return fmt.Sprint(value)
		}
	}
	return ""
}

func (r *Runtime) loadSourceJSLib(ctx context.Context, vm *goja.Runtime, src *domainsource.BookSource) error {
	if src == nil || strings.TrimSpace(src.JSLib) == "" {
		return nil
	}
	cancelInterrupt := r.installInterrupt(ctx, vm)
	defer cancelInterrupt()
	if _, err := vm.RunString(rewriteJavaCompatScript(src.JSLib)); err != nil {
		return r.wrapError(ErrScriptFailed, err)
	}
	return nil
}

func (r *Runtime) installSafeGlobals(vm *goja.Runtime, src *domainsource.BookSource, runCtx evalContext) error {
	blocked := []string{"require", "process", "global", "globalThis", "window", "document", "XMLHttpRequest", "fetch"}
	for _, name := range blocked {
		if err := vm.Set(name, goja.Undefined()); err != nil {
			return err
		}
	}
	if r.policy.AllowLog {
		if err := vm.Set("log", func(args ...any) {
			r.mu.Lock()
			defer r.mu.Unlock()
			text := fmt.Sprint(args...)
			r.logs = append(r.logs, logging.RedactString(text))
		}); err != nil {
			return err
		}
	}
	if r.cache == nil {
		r.cache = NewNamespacedCache()
	}
	if err := vm.Set("cache", map[string]any{
		"get": func(key string) any {
			return r.cache.Get(runCtx.sourceKey, key)
		},
		"put": func(key string, value any) {
			r.cache.Put(runCtx.sourceKey, key, value)
		},
	}); err != nil {
		return err
	}
	if err := vm.Set("java", r.javaBindings(vm, src, runCtx)); err != nil {
		return err
	}
	if _, err := vm.RunString(`
if (!java.ajaxAll) {
  java.ajaxAll = function(urls) {
    if (!urls || !urls.map) return [];
    return urls.map(function(u) { return java.connect(u); });
  };
}
`); err != nil {
		return err
	}
	if err := installJavaImporterCompat(vm); err != nil {
		return err
	}
	if r.cookie == nil {
		r.cookie = NewMemoryCookieStore(r.policy.Cookie)
	}
	if err := vm.Set("cookie", r.cookie.Bindings()); err != nil {
		return err
	}
	return nil
}

func (r *Runtime) javaBindings(vm *goja.Runtime, src *domainsource.BookSource, runCtx evalContext) map[string]any {
	return map[string]any{
		"base64Encode": func(value any) string {
			return base64.StdEncoding.EncodeToString([]byte(fmt.Sprint(value)))
		},
		"getWebViewUA": func() string {
			return defaultWebViewUserAgent
		},
		"t2s": func(value any) string {
			return convertChinese("t2s", fmt.Sprint(value))
		},
		"s2t": func(value any) string {
			return convertChinese("s2t", fmt.Sprint(value))
		},
		"base64Decode": func(value any) string {
			decoded, err := base64.StdEncoding.DecodeString(fmt.Sprint(value))
			if err != nil {
				return ""
			}
			return string(decoded)
		},
		"md5Encode": func(value any) string {
			sum := md5.Sum([]byte(fmt.Sprint(value)))
			return hex.EncodeToString(sum[:])
		},
		"HMacHex": func(data any, algorithm any, key any) string {
			return hmacHex(fmt.Sprint(data), fmt.Sprint(algorithm), fmt.Sprint(key))
		},
		"hexDecodeToString": func(value any) string {
			decoded, err := hex.DecodeString(fmt.Sprint(value))
			if err != nil {
				return ""
			}
			return string(decoded)
		},
		"hexEncodeToString": func(value any) string {
			return hex.EncodeToString([]byte(fmt.Sprint(value)))
		},
		"createSymmetricCrypto": func(transformation any, key any, iv ...any) map[string]any {
			var ivValue any
			if len(iv) > 0 {
				ivValue = iv[0]
			}
			return newSymmetricCryptoCompat(fmt.Sprint(transformation), key, ivValue)
		},
		"aesBase64DecodeToString": func(data any, key any, transformation any, iv any) string {
			decoded, err := aesBase64DecodeToString(fmt.Sprint(data), fmt.Sprint(key), fmt.Sprint(transformation), fmt.Sprint(iv))
			if err != nil {
				return ""
			}
			return decoded
		},
		"desEncodeToBase64String": func(data any, key any, transformation any, iv any) string {
			encoded, err := desEncodeToBase64String(fmt.Sprint(data), fmt.Sprint(key), fmt.Sprint(transformation), fmt.Sprint(iv))
			if err != nil {
				return ""
			}
			return encoded
		},
		"encodeURI": func(value any, charsetArgs ...any) string {
			charsetName := ""
			if len(charsetArgs) > 0 {
				charsetName = fmt.Sprint(charsetArgs[0])
			}
			return urlrule.EncodeURI(fmt.Sprint(value), charsetName)
		},
		"randomUUID": func() *goja.Object {
			return javaStringCompat(vm, randomUUIDString())
		},
		"timeFormatUTC": func(value any, pattern any, offsetHours any) string {
			t, ok := coerceTimeValue(value)
			if !ok {
				return ""
			}
			offset := 0
			switch v := offsetHours.(type) {
			case int:
				offset = v
			case int64:
				offset = int(v)
			case float64:
				offset = int(v)
			case string:
				parsed, _ := strconv.Atoi(strings.TrimSpace(v))
				offset = parsed
			}
			loc := time.FixedZone("UTCOffset", offset*3600)
			return t.In(loc).Format(javaTimeLayout(fmt.Sprint(pattern)))
		},
		"toast":     func(args ...any) {},
		"longToast": func(args ...any) {},
		"put": func(key string, value any) any {
			if r.cache == nil {
				r.cache = NewNamespacedCache()
			}
			r.cache.Put(runCtx.sourceKey, key, value)
			return value
		},
		"get": func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				return goja.Undefined()
			}
			if len(call.Arguments) == 1 && !looksLikeURL(call.Argument(0).String()) {
				if value, ok := getRuntimeVariable(vm, call.Argument(0).String()); ok {
					return vm.ToValue(value)
				}
				if r.cache == nil {
					return vm.ToValue("")
				}
				value := r.cache.Get(runCtx.sourceKey, call.Argument(0).String())
				if value == nil {
					return vm.ToValue("")
				}
				return vm.ToValue(value)
			}
			resp, err := r.fetchScriptCall(context.Background(), vm, src, "GET", call)
			return newScriptHTTPResponse(vm, resp, err)
		},
		"getString": func(rule any) string {
			var fallback any
			if srcValue := vm.Get("src"); srcValue != nil && !goja.IsUndefined(srcValue) && !goja.IsNull(srcValue) {
				fallback = srcValue.Export()
			}
			return scriptGetString(vm, vm.Get("result").Export(), fallback, fmt.Sprint(rule))
		},
		"getElements": func(rule any) []any {
			return scriptGetElements(vm, vm.Get("result").Export(), fmt.Sprint(rule))
		},
		"post": func(call goja.FunctionCall) goja.Value {
			resp, err := r.fetchScriptCall(context.Background(), vm, src, "POST", call)
			return newScriptHTTPResponse(vm, resp, err)
		},
		"ajax": func(raw any) string {
			resp, err := r.fetchScriptURL(context.Background(), vm, src, raw, nil)
			if err != nil {
				return err.Error()
			}
			return string(resp.Body)
		},
		"connect": func(call goja.FunctionCall) goja.Value {
			resp, err := r.fetchScriptCall(context.Background(), vm, src, "GET", call)
			return newScriptHTTPResponse(vm, resp, err)
		},
		"log": func(args ...any) {
			if !r.policy.AllowLog {
				return
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			r.logs = append(r.logs, logging.RedactString(fmt.Sprint(args...)))
		},
	}
}

func installJavaImporterCompat(vm *goja.Runtime) error {
	if _, err := vm.RunString(`
if (!String.prototype.getBytes) {
  Object.defineProperty(String.prototype, "getBytes", {
    value: function(){
      var s = this.toString();
      var out = new Array(s.length);
      for (var i = 0; i < s.length; i++) out[i] = s.charCodeAt(i) & 255;
      return out;
    },
    configurable: true
  });
}
if (!String.prototype.body) {
  Object.defineProperty(String.prototype, "body", {
    value: function(){ return this.toString(); },
    configurable: true
  });
}
if (!String.prototype.url) {
  Object.defineProperty(String.prototype, "url", {
    value: function(){ return ""; },
    configurable: true
  });
}
if (!String.prototype.code) {
  Object.defineProperty(String.prototype, "code", {
    value: function(){ return 200; },
    configurable: true
  });
}
if (!String.prototype.header) {
  Object.defineProperty(String.prototype, "header", {
    value: function(){ return ""; },
    configurable: true
  });
}
if (!String.prototype.headers) {
  Object.defineProperty(String.prototype, "headers", {
    value: function(){ return { get: function(){ return ""; } }; },
    configurable: true
  });
}
`); err != nil {
		return err
	}
	if err := vm.Set("JavaImporter", func(call goja.ConstructorCall) *goja.Object {
		obj := vm.GlobalObject()
		_ = obj.Set("importPackage", func(args ...any) {})
		return obj
	}); err != nil {
		return err
	}
	packages := vm.NewObject()
	javaPkg := vm.NewObject()
	javaLangPkg := vm.NewObject()
	javaIoPkg := vm.NewObject()
	javaUtilPkg := vm.NewObject()
	javaUtilUUIDPkg := vm.NewObject()
	javaUtilZipPkg := vm.NewObject()
	javaxPkg := vm.NewObject()
	javaxCryptoPkg := vm.NewObject()
	javaxCryptoSpecPkg := vm.NewObject()
	okhttpPkg := vm.NewObject()
	androidPkg := vm.NewObject()
	androidOsPkg := vm.NewObject()
	androidBuildPkg := vm.NewObject()
	cnPkg := vm.NewObject()
	hutoolPkg := vm.NewObject()
	hutoolCorePkg := vm.NewObject()
	hutoolCoreUtilPkg := vm.NewObject()
	hutoolCoreCodecPkg := vm.NewObject()
	hutoolCryptoPkg := vm.NewObject()
	hutoolCryptoDigestPkg := vm.NewObject()
	if err := javaUtilUUIDPkg.Set("randomUUID", func() *goja.Object {
		return javaStringCompat(vm, randomUUIDString())
	}); err != nil {
		return err
	}
	if err := javaUtilPkg.Set("UUID", javaUtilUUIDPkg); err != nil {
		return err
	}
	if err := javaUtilPkg.Set("zip", javaUtilZipPkg); err != nil {
		return err
	}
	if err := javaPkg.Set("lang", javaLangPkg); err != nil {
		return err
	}
	if err := javaPkg.Set("io", javaIoPkg); err != nil {
		return err
	}
	if err := javaPkg.Set("util", javaUtilPkg); err != nil {
		return err
	}
	if err := javaxCryptoPkg.Set("spec", javaxCryptoSpecPkg); err != nil {
		return err
	}
	if err := javaxPkg.Set("crypto", javaxCryptoPkg); err != nil {
		return err
	}
	if err := androidBuildPkg.Set("MODEL", "Reader"); err != nil {
		return err
	}
	if err := androidBuildPkg.Set("MANUFACTURER", "Reader"); err != nil {
		return err
	}
	if err := androidOsPkg.Set("Build", androidBuildPkg); err != nil {
		return err
	}
	if err := androidPkg.Set("os", androidOsPkg); err != nil {
		return err
	}
	if err := hutoolCorePkg.Set("util", hutoolCoreUtilPkg); err != nil {
		return err
	}
	if err := hutoolCorePkg.Set("codec", hutoolCoreCodecPkg); err != nil {
		return err
	}
	if err := hutoolCryptoPkg.Set("digest", hutoolCryptoDigestPkg); err != nil {
		return err
	}
	if err := hutoolPkg.Set("core", hutoolCorePkg); err != nil {
		return err
	}
	if err := hutoolPkg.Set("crypto", hutoolCryptoPkg); err != nil {
		return err
	}
	if err := cnPkg.Set("hutool", hutoolPkg); err != nil {
		return err
	}
	if err := packages.Set("android", androidPkg); err != nil {
		return err
	}
	if err := packages.Set("okhttp3", okhttpPkg); err != nil {
		return err
	}
	if err := packages.Set("java", javaPkg); err != nil {
		return err
	}
	if err := packages.Set("javax", javaxPkg); err != nil {
		return err
	}
	if err := packages.Set("cn", cnPkg); err != nil {
		return err
	}
	if err := vm.Set("Packages", packages); err != nil {
		return err
	}
	if err := vm.Set("DigestUtil", map[string]any{
		"md5Hex": func(value any) string {
			sum := md5.Sum([]byte(fmt.Sprint(value)))
			return hex.EncodeToString(sum[:])
		},
	}); err != nil {
		return err
	}
	if err := vm.Set("StrUtil", map[string]any{
		"reverse": func(value any) string {
			in := []rune(fmt.Sprint(value))
			for i, j := 0, len(in)-1; i < j; i, j = i+1, j-1 {
				in[i], in[j] = in[j], in[i]
			}
			return string(in)
		},
	}); err != nil {
		return err
	}
	if err := vm.Set("ZipUtil", map[string]any{
		"gzip": func(value any, _ any) []byte {
			var buf bytes.Buffer
			writer := gzip.NewWriter(&buf)
			_, _ = writer.Write([]byte(fmt.Sprint(value)))
			_ = writer.Close()
			return buf.Bytes()
		},
	}); err != nil {
		return err
	}
	if err := vm.Set("Base64", map[string]any{
		"getDecoder": func() map[string]any {
			return map[string]any{
				"decode": func(value any) []byte {
					decoded, err := base64.StdEncoding.DecodeString(fmt.Sprint(value))
					if err != nil {
						return nil
					}
					return decoded
				},
			}
		},
	}); err != nil {
		return err
	}
	if err := vm.Set("Arrays", map[string]any{
		"copyOfRange": func(value any, start int, end int) []byte {
			data := bytesFromJS(value)
			if start < 0 {
				start = 0
			}
			if end > len(data) {
				end = len(data)
			}
			if start > end {
				start = end
			}
			return append([]byte(nil), data[start:end]...)
		},
	}); err != nil {
		return err
	}
	if err := vm.Set("SecretKeySpec", func(value any, algorithm any) map[string]any {
		return map[string]any{"key": bytesFromJS(value), "algorithm": fmt.Sprint(algorithm)}
	}); err != nil {
		return err
	}
	if err := vm.Set("IvParameterSpec", func(value any) map[string]any {
		return map[string]any{"iv": bytesFromJS(value)}
	}); err != nil {
		return err
	}
	if err := vm.Set("Cipher", map[string]any{
		"getInstance": func(transformation any) map[string]any {
			state := &javaCipherCompat{transformation: fmt.Sprint(transformation)}
			return map[string]any{
				"init":    state.init,
				"doFinal": state.doFinal,
			}
		},
	}); err != nil {
		return err
	}
	if err := vm.Set("ByteArrayInputStream", func(value any) map[string]any {
		return map[string]any{
			"data":  bytesFromJS(value),
			"close": func() {},
		}
	}); err != nil {
		return err
	}
	if err := vm.Set("ByteArrayOutputStream", func() map[string]any {
		stream := &javaByteArrayOutputStream{}
		return map[string]any{
			"write":    stream.write,
			"close":    stream.close,
			"toString": stream.String,
		}
	}); err != nil {
		return err
	}
	if err := vm.Set("GZIPInputStream", func(input any) map[string]any {
		stream := newJavaGZIPInputStream(input)
		return map[string]any{
			"read":  stream.read,
			"close": stream.close,
		}
	}); err != nil {
		return err
	}
	if _, err := vm.RunString(`
var __ReaderByteArrayInputStream = ByteArrayInputStream;
var __ReaderByteArrayOutputStream = ByteArrayOutputStream;
var __ReaderGZIPInputStream = GZIPInputStream;
ByteArrayInputStream = function(value){ return __ReaderByteArrayInputStream(value); };
ByteArrayOutputStream = function(){ return __ReaderByteArrayOutputStream(); };
GZIPInputStream = function(value){ return __ReaderGZIPInputStream(value); };
`); err != nil {
		return err
	}
	return nil
}

var javaImporterFunctionPattern = regexp.MustCompile(`function\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
var rhinoLetConditionPattern = regexp.MustCompile(`\blet\s*\(\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*([^)]*?)\s*\)\s*([^)\r\n]+)`)

func rewriteJavaCompatScript(script string) string {
	script = rhinoLetConditionPattern.ReplaceAllString(script, `($1 = $2, $3)`)
	if !strings.Contains(script, "JavaImporter") {
		return script
	}
	return javaImporterFunctionPattern.ReplaceAllString(script, `$1 = function(`)
}

type javaCipherCompat struct {
	transformation string
	key            []byte
	iv             []byte
}

type symmetricCryptoCompat struct {
	transformation string
	key            []byte
	iv             []byte
}

func newSymmetricCryptoCompat(transformation string, key any, iv any) map[string]any {
	state := &symmetricCryptoCompat{
		transformation: strings.TrimSpace(transformation),
		key:            bytesFromJS(key),
		iv:             bytesFromJS(iv),
	}
	return map[string]any{
		"decrypt":       state.decrypt,
		"decryptStr":    state.decryptStr,
		"encrypt":       state.encrypt,
		"encryptBase64": state.encryptBase64,
		"encryptHex":    state.encryptHex,
	}
}

func (c *symmetricCryptoCompat) decrypt(value any) []byte {
	plain, err := c.decryptBytes(value)
	if err != nil {
		return nil
	}
	return plain
}

func (c *symmetricCryptoCompat) decryptStr(value any) string {
	plain, err := c.decryptBytes(value)
	if err != nil {
		return ""
	}
	return string(plain)
}

func (c *symmetricCryptoCompat) encrypt(value any) []byte {
	cipherText, err := c.encryptBytes(value)
	if err != nil {
		return nil
	}
	return cipherText
}

func (c *symmetricCryptoCompat) encryptBase64(value any) string {
	cipherText, err := c.encryptBytes(value)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(cipherText)
}

func (c *symmetricCryptoCompat) encryptHex(value any) string {
	cipherText, err := c.encryptBytes(value)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(cipherText)
}

func (c *symmetricCryptoCompat) decryptBytes(value any) ([]byte, error) {
	return decryptSymmetricBytes(decodeSymmetricInput(value), c.transformation, c.key, c.iv)
}

func (c *symmetricCryptoCompat) encryptBytes(value any) ([]byte, error) {
	return encryptSymmetricBytes(bytesFromJS(value), c.transformation, c.key, c.iv)
}

func (c *javaCipherCompat) init(mode int, keySpec map[string]any, ivSpec map[string]any) {
	c.key = bytesFromJS(keySpec["key"])
	c.iv = bytesFromJS(ivSpec["iv"])
}

func (c *javaCipherCompat) doFinal(value any) string {
	decoded, err := aesCBCDecrypt(bytesFromJS(value), c.key, c.iv, c.transformation)
	if err != nil {
		return ""
	}
	return string(decoded)
}

type javaByteArrayOutputStream struct {
	buf bytes.Buffer
}

func (s *javaByteArrayOutputStream) write(value goja.Value, off int, length int) {
	data := bytesFromJS(value)
	if off < 0 {
		off = 0
	}
	if length < 0 {
		length = 0
	}
	if off > len(data) {
		return
	}
	end := off + length
	if end > len(data) {
		end = len(data)
	}
	_, _ = s.buf.Write(data[off:end])
}

func (s *javaByteArrayOutputStream) close() {}

func (s *javaByteArrayOutputStream) String() string {
	return s.buf.String()
}

func (s *javaByteArrayOutputStream) ToString() string {
	return s.buf.String()
}

type javaGZIPInputStream struct {
	data []byte
	pos  int
}

func newJavaGZIPInputStream(input any) *javaGZIPInputStream {
	data := []byte(nil)
	if m, ok := input.(map[string]any); ok {
		data = bytesFromJS(m["data"])
	} else {
		data = bytesFromJS(input)
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return &javaGZIPInputStream{}
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return &javaGZIPInputStream{}
	}
	return &javaGZIPInputStream{data: decoded}
}

func (s *javaGZIPInputStream) read(target goja.Value) int {
	buf := bytesFromJS(target)
	if len(buf) == 0 || s.pos >= len(s.data) {
		return -1
	}
	n := copy(buf, s.data[s.pos:])
	s.pos += n
	writeBytesToJSArray(target, buf[:n])
	return n
}

func (s *javaGZIPInputStream) close() {}

func aesCBCDecrypt(cipherText []byte, key []byte, iv []byte, transformation string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != block.BlockSize() || len(cipherText)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("invalid AES/CBC input")
	}
	plain := make([]byte, len(cipherText))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, cipherText)
	if strings.Contains(strings.ToUpper(transformation), "PKCS5") || strings.Contains(strings.ToUpper(transformation), "PKCS7") {
		plain, err = pkcs7Unpad(plain, block.BlockSize())
		if err != nil {
			return nil, err
		}
	}
	return plain, nil
}

func decodeSymmetricInput(value any) []byte {
	switch typed := value.(type) {
	case string:
		if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(typed)); err == nil {
			return decoded
		}
		if decoded, err := hex.DecodeString(strings.TrimSpace(typed)); err == nil {
			return decoded
		}
		return []byte(typed)
	default:
		return bytesFromJS(value)
	}
}

func encryptSymmetricBytes(plain []byte, transformation string, key []byte, iv []byte) ([]byte, error) {
	block, algorithm, err := newSymmetricBlock(transformation, key)
	if err != nil {
		return nil, err
	}
	modeName, paddingName := parseSymmetricTransformation(transformation, defaultModeForAlgorithm(algorithm), "PKCS5PADDING")
	switch paddingName {
	case "PKCS5PADDING", "PKCS7PADDING":
		plain = pkcs7Pad(plain, block.BlockSize())
	case "NOPADDING":
		if len(plain)%block.BlockSize() != 0 {
			return nil, fmt.Errorf("invalid plaintext length")
		}
	default:
		return nil, fmt.Errorf("unsupported symmetric padding %s", paddingName)
	}
	cipherText := make([]byte, len(plain))
	switch modeName {
	case "ECB":
		for start := 0; start < len(plain); start += block.BlockSize() {
			block.Encrypt(cipherText[start:start+block.BlockSize()], plain[start:start+block.BlockSize()])
		}
	case "CBC":
		if len(iv) != block.BlockSize() {
			return nil, fmt.Errorf("invalid iv length")
		}
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(cipherText, plain)
	default:
		return nil, fmt.Errorf("unsupported symmetric mode %s", modeName)
	}
	return cipherText, nil
}

func decryptSymmetricBytes(cipherText []byte, transformation string, key []byte, iv []byte) ([]byte, error) {
	block, algorithm, err := newSymmetricBlock(transformation, key)
	if err != nil {
		return nil, err
	}
	modeName, paddingName := parseSymmetricTransformation(transformation, defaultModeForAlgorithm(algorithm), "PKCS5PADDING")
	if len(cipherText)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("invalid ciphertext length")
	}
	plain := make([]byte, len(cipherText))
	switch modeName {
	case "ECB":
		for start := 0; start < len(cipherText); start += block.BlockSize() {
			block.Decrypt(plain[start:start+block.BlockSize()], cipherText[start:start+block.BlockSize()])
		}
	case "CBC":
		if len(iv) != block.BlockSize() {
			return nil, fmt.Errorf("invalid iv length")
		}
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, cipherText)
	default:
		return nil, fmt.Errorf("unsupported symmetric mode %s", modeName)
	}
	switch paddingName {
	case "PKCS5PADDING", "PKCS7PADDING":
		return pkcs7Unpad(plain, block.BlockSize())
	case "NOPADDING":
		return plain, nil
	default:
		return nil, fmt.Errorf("unsupported symmetric padding %s", paddingName)
	}
}

func newSymmetricBlock(transformation string, key []byte) (cipher.Block, string, error) {
	algorithm := parseSymmetricAlgorithm(transformation)
	switch algorithm {
	case "AES":
		block, err := aes.NewCipher(key)
		return block, algorithm, err
	case "DES":
		block, err := des.NewCipher(key)
		return block, algorithm, err
	case "DESEDE", "3DES", "TRIPLEDES":
		block, err := des.NewTripleDESCipher(key)
		return block, algorithm, err
	default:
		return nil, algorithm, fmt.Errorf("unsupported symmetric algorithm %s", algorithm)
	}
}

func parseSymmetricAlgorithm(transformation string) string {
	parts := strings.Split(strings.ToUpper(strings.TrimSpace(transformation)), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "AES"
	}
	return parts[0]
}

func defaultModeForAlgorithm(algorithm string) string {
	if algorithm == "AES" {
		return "CBC"
	}
	return "ECB"
}

func (r *Runtime) fetchScriptURL(ctx context.Context, vm *goja.Runtime, src *domainsource.BookSource, raw any, extraHeaders map[string]string) (*fetch.Response, error) {
	if r.policy.Fetcher == nil {
		return nil, ErrScriptFailed
	}
	req, err := r.buildScriptRequest(vm, src, fmt.Sprint(raw), extraHeaders)
	if err != nil {
		return nil, err
	}
	return r.policy.Fetcher.Fetch(ctx, req)
}

func scriptGetString(vm *goja.Runtime, input any, fallback any, rule string) string {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return ""
	}
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		RedirectURL: scriptAnalyzerBaseURL(vm),
		SourceURL:   scriptAnalyzerSourceURL(vm),
	})
	if value, err := analyzer.GetString(context.Background(), rule, input); err == nil && value != "" {
		return value
	}
	if fallback != nil {
		if value, err := analyzer.GetString(context.Background(), rule, fallback); err == nil {
			return value
		}
	}
	return ""
}

func scriptGetElements(vm *goja.Runtime, input any, rule string) []any {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return nil
	}
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		RedirectURL: scriptAnalyzerBaseURL(vm),
		SourceURL:   scriptAnalyzerSourceURL(vm),
	})
	elements, err := analyzer.GetElements(context.Background(), rule, input)
	if err != nil {
		return nil
	}
	out := make([]any, 0)
	for _, element := range elements {
		if sel, ok := element.(*goquery.Selection); ok {
			out = append(out, newSelectionBinding(sel))
			continue
		}
		out = append(out, element)
	}
	return out
}

func scriptAnalyzerBaseURL(vm *goja.Runtime) string {
	if vm == nil {
		return ""
	}
	value := vm.Get("baseUrl")
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value.Export()))
}

func scriptAnalyzerSourceURL(vm *goja.Runtime) string {
	if vm == nil {
		return ""
	}
	value := vm.Get("source")
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	raw, ok := value.Export().(map[string]any)
	if !ok {
		return ""
	}
	if sourceURL, ok := raw["bookSourceUrl"]; ok && strings.TrimSpace(fmt.Sprint(sourceURL)) != "" {
		return strings.TrimSpace(fmt.Sprint(sourceURL))
	}
	if key, ok := raw["key"]; ok {
		return strings.TrimSpace(fmt.Sprint(key))
	}
	return ""
}

func newSelectionBinding(sel *goquery.Selection) map[string]any {
	return map[string]any{
		"__selection": sel,
		"select": func(rule string) map[string]any {
			return newSelectionBinding(sel.Find(rule).First())
		},
		"parentNode": func() map[string]any {
			return newSelectionBinding(sel.Parent().First())
		},
		"text": func() string {
			return strings.TrimSpace(sel.Text())
		},
		"attr": func(name string) string {
			value, _ := sel.Attr(name)
			return value
		},
		"html": func() string {
			value, _ := sel.Html()
			return value
		},
	}
}

func scriptInputString(input any) string {
	switch value := input.(type) {
	case nil:
		return ""
	case string:
		return value
	case []byte:
		return string(value)
	default:
		raw, err := json.Marshal(value)
		if err == nil {
			return string(raw)
		}
		return fmt.Sprint(value)
	}
}

func getRuntimeVariable(vm *goja.Runtime, key string) (any, bool) {
	if value, ok := getVariableFromJSObject(vm, vm.Get("__variables"), key); ok {
		return value, true
	}
	for _, bindingName := range []string{"chapter", "book"} {
		binding := vm.Get(bindingName)
		if binding == nil || goja.IsUndefined(binding) || goja.IsNull(binding) {
			continue
		}
		if value, ok := getVariableFromBinding(binding.Export(), key); ok {
			return value, true
		}
	}
	return nil, false
}

func getVariableFromBinding(value any, key string) (any, bool) {
	if value, ok := getVariableFromMap(value, key); ok {
		return value, true
	}
	return getVariableFromStruct(value, key)
}

func getVariableFromMap(value any, key string) (any, bool) {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	variableRaw, ok := raw["variable"].(string)
	if !ok || strings.TrimSpace(variableRaw) == "" {
		return nil, false
	}
	vars := map[string]any{}
	if err := json.Unmarshal([]byte(variableRaw), &vars); err != nil {
		return nil, false
	}
	value, ok = vars[key]
	return value, ok
}

func putRuntimeVariable(vm *goja.Runtime, key string, value any) {
	vars := vm.Get("__variables")
	if goja.IsUndefined(vars) || goja.IsNull(vars) {
		obj := vm.NewObject()
		_ = vm.Set("__variables", obj)
		vars = obj
	}
	if obj := vars.ToObject(vm); obj != nil {
		_ = obj.Set(key, value)
	}
}

func getVariableFromJSObject(vm *goja.Runtime, value goja.Value, key string) (any, bool) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, false
	}
	exported := value.Export()
	if vars, ok := exported.(map[string]any); ok {
		got, ok := vars[key]
		return got, ok
	}
	if vars, ok := exported.(map[string]interface{}); ok {
		got, ok := vars[key]
		return got, ok
	}
	obj := value.ToObject(vm)
	got := obj.Get(key)
	return got.Export(), !goja.IsUndefined(got) && !goja.IsNull(got)
}

func getVariableFromStruct(value any, key string) (any, bool) {
	if value == nil {
		return nil, false
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, false
	}
	field := rv.FieldByName("Variable")
	if !field.IsValid() || field.Kind() != reflect.String {
		return nil, false
	}
	vars := map[string]any{}
	if err := json.Unmarshal([]byte(field.String()), &vars); err != nil {
		return nil, false
	}
	value, ok := vars[key]
	return value, ok
}

func compatGetVariable(raw *string, key string) string {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return ""
	}
	vars := map[string]any{}
	if err := json.Unmarshal([]byte(*raw), &vars); err != nil {
		return ""
	}
	return fmt.Sprint(vars[key])
}

func compatPutVariable(raw *string, key string, value any) string {
	if raw == nil {
		return fmt.Sprint(value)
	}
	vars := map[string]any{}
	if strings.TrimSpace(*raw) != "" {
		_ = json.Unmarshal([]byte(*raw), &vars)
	}
	vars[key] = value
	if encoded, err := json.Marshal(vars); err == nil {
		*raw = string(encoded)
	}
	return fmt.Sprint(value)
}

func bytesFromJS(value any) []byte {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		return append([]byte(nil), typed...)
	case string:
		return []byte(typed)
	case goja.Value:
		if goja.IsUndefined(typed) || goja.IsNull(typed) {
			return nil
		}
		if obj := typed.ToObject(nil); obj != nil {
			if lengthValue := obj.Get("length"); !goja.IsUndefined(lengthValue) {
				length := int(lengthValue.ToInteger())
				out := make([]byte, 0, length)
				for i := 0; i < length; i++ {
					out = append(out, byte(obj.Get(strconv.Itoa(i)).ToInteger()))
				}
				return out
			}
		}
		return bytesFromJS(typed.Export())
	case []any:
		out := make([]byte, 0, len(typed))
		for _, item := range typed {
			out = append(out, byteFromAny(item))
		}
		return out
	case map[string]any:
		if raw, ok := typed["key"]; ok {
			return bytesFromJS(raw)
		}
		if raw, ok := typed["iv"]; ok {
			return bytesFromJS(raw)
		}
		return nil
	default:
		return []byte(fmt.Sprint(value))
	}
}

func byteFromAny(value any) byte {
	switch typed := value.(type) {
	case int:
		return byte(typed)
	case int64:
		return byte(typed)
	case float64:
		return byte(int(typed))
	case byte:
		return typed
	case goja.Value:
		return byte(typed.ToInteger())
	default:
		return byte(0)
	}
}

func writeBytesToJSArray(target goja.Value, data []byte) {
	if goja.IsUndefined(target) || goja.IsNull(target) {
		return
	}
	obj := target.ToObject(nil)
	if obj == nil {
		return
	}
	for i, b := range data {
		_ = obj.Set(strconv.Itoa(i), int(b))
	}
}

func (r *Runtime) fetchScriptCall(ctx context.Context, vm *goja.Runtime, src *domainsource.BookSource, method string, call goja.FunctionCall) (*fetch.Response, error) {
	if r.policy.Fetcher == nil {
		return nil, ErrScriptFailed
	}
	raw := call.Argument(0).String()
	req, err := r.buildScriptRequest(vm, src, raw, nil)
	if err != nil {
		return nil, err
	}
	switch method {
	case "POST":
		req.Method = "POST"
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
			req.Body = []byte(call.Argument(1).String())
		}
		if len(call.Arguments) > 2 && !goja.IsUndefined(call.Argument(2)) && !goja.IsNull(call.Argument(2)) {
			mergeHeaders(req.Headers, normalizeScriptOptions(call.Argument(2).Export()))
		}
	default:
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
			mergeHeaders(req.Headers, normalizeScriptOptions(call.Argument(1).Export()))
		}
	}
	return r.policy.Fetcher.Fetch(ctx, req)
}

type scriptHTTPRequest struct {
	requestURL string
}

func (r scriptHTTPRequest) URL() string {
	return r.requestURL
}

func (r scriptHTTPRequest) Url() string {
	return r.requestURL
}

type scriptHTTPRawResponse struct {
	request scriptHTTPRequest
}

func (r scriptHTTPRawResponse) Request() scriptHTTPRequest {
	return r.request
}

func newScriptHTTPResponse(vm *goja.Runtime, resp *fetch.Response, err error) goja.Value {
	errorText := ""
	if err != nil {
		errorText = err.Error()
	}
	if resp == nil {
		resp = &fetch.Response{StatusCode: 0, Headers: http.Header{}, Body: []byte(errorText)}
	}
	bodyText := string(resp.Body)
	requestURL := resp.URL
	obj := vm.NewObject()
	bodyValue := callableString(vm, bodyText)
	urlValue := callableString(vm, requestURL)
	headersValue := callableHeaders(vm, resp.Headers)
	_ = obj.Set("body", bodyValue)
	_ = obj.Set("url", urlValue)
	_ = obj.Set("statusCode", resp.StatusCode)
	_ = obj.Set("headers", headersValue)
	_ = obj.Set("code", func() int {
		return resp.StatusCode
	})
	_ = obj.Set("message", func() string {
		if errorText != "" {
			return errorText
		}
		return http.StatusText(resp.StatusCode)
	})
	_ = obj.Set("header", func(name string) string {
		if resp.Headers == nil {
			return ""
		}
		return resp.Headers.Get(name)
	})
	_ = obj.Set("isSuccessful", func() bool {
		return resp.StatusCode >= 200 && resp.StatusCode < 300
	})
	_ = obj.Set("raw", func() map[string]any {
		return map[string]any{
			"request": func() map[string]any {
				return map[string]any{
					"url": callableString(vm, requestURL),
				}
			},
		}
	})
	return obj
}

func callableString(vm *goja.Runtime, value string) *goja.Object {
	fn, err := vm.RunString("(function(v){ var f = function(){ return v; }; f.toString = function(){ return v; }; f.valueOf = function(){ return v; }; return f; })")
	if err != nil {
		panic(err)
	}
	callable, ok := goja.AssertFunction(fn)
	if !ok {
		panic(ErrScriptFailed)
	}
	out, err := callable(goja.Undefined(), vm.ToValue(value))
	if err != nil {
		panic(err)
	}
	obj, ok := out.(*goja.Object)
	if !ok {
		panic(ErrScriptFailed)
	}
	return obj
}

func callableHeaders(vm *goja.Runtime, headers http.Header) *goja.Object {
	fn, err := vm.RunString(`(function(h){
  var f = function(){ return f; };
  f.get = function(name){ return (h && h[name] && h[name][0]) || (h && h[String(name).toLowerCase()] && h[String(name).toLowerCase()][0]) || ""; };
  f.toString = function(){ return "[headers]"; };
  return f;
})`)
	if err != nil {
		panic(err)
	}
	callable, ok := goja.AssertFunction(fn)
	if !ok {
		panic(ErrScriptFailed)
	}
	headerMap := map[string][]string{}
	for key, values := range headers {
		headerMap[key] = values
		headerMap[strings.ToLower(key)] = values
	}
	out, err := callable(goja.Undefined(), vm.ToValue(headerMap))
	if err != nil {
		panic(err)
	}
	obj, ok := out.(*goja.Object)
	if !ok {
		panic(ErrScriptFailed)
	}
	return obj
}

func (r *Runtime) buildScriptRequest(vm *goja.Runtime, src *domainsource.BookSource, raw string, extraHeaders map[string]string) (fetch.Request, error) {
	expanded, err := r.expandScriptTemplates(vm, raw)
	if err != nil {
		return fetch.Request{}, err
	}
	raw = expanded
	custom := parseCustomURL(raw)
	if err := validateRuntimeSupportedURLOptions(custom.Attribute, "method", "body", "headers", "type", "charset", "origin", "retry"); err != nil {
		return fetch.Request{}, r.wrapError(ErrScriptFailed, err)
	}
	baseURL := r.policy.BaseURL
	if baseURL == "" && src != nil {
		baseURL = stripSourceSuffix(src.BookSourceURL)
	}
	targetURL := urlrule.Absolute(baseURL, custom.URL)
	if targetURL == "" {
		targetURL = custom.URL
	}
	req := fetch.Request{URL: targetURL, Method: "GET", Headers: map[string]string{}, UseCookieJar: src != nil && src.EnabledCookieJar}
	if src != nil {
		headers, err := runtimeHeaderMap(vm, src)
		if err != nil {
			return fetch.Request{}, r.wrapError(ErrScriptFailed, err)
		}
		for key, value := range headers {
			req.Headers[key] = value
		}
	}
	for key, value := range extraHeaders {
		req.Headers[key] = value
	}
	applyURLOptions(&req, custom.Attribute)
	return req, nil
}

func runtimeHeaderMap(vm *goja.Runtime, src *domainsource.BookSource) (map[string]string, error) {
	if src == nil {
		return map[string]string{}, nil
	}
	header := strings.TrimSpace(src.Header)
	headers := map[string]string{}
	switch {
	case header == "":
	case strings.HasPrefix(strings.ToLower(header), "@js:"):
		value, err := vm.RunString(rewriteJavaCompatScript(strings.TrimSpace(header[4:])))
		if err != nil {
			return nil, err
		}
		for key, headerValue := range parseHeaderJSONString(fmt.Sprint(value.Export())) {
			headers[key] = headerValue
		}
	case strings.HasPrefix(strings.ToLower(header), "<js>") && strings.HasSuffix(strings.ToLower(header), "</js>"):
		value, err := vm.RunString(rewriteJavaCompatScript(strings.TrimSpace(header[4 : len(header)-5])))
		if err != nil {
			return nil, err
		}
		for key, headerValue := range parseHeaderJSONString(fmt.Sprint(value.Export())) {
			headers[key] = headerValue
		}
	default:
		for key, value := range parseHeaderJSONString(src.Header) {
			headers[key] = value
		}
	}
	for key, value := range parseHeaderJSONString(loadSourceCompatState(stripSourceSuffix(src.BookSourceURL)).LoginHeader) {
		headers[key] = value
	}
	return headers, nil
}

func (r *Runtime) expandScriptTemplates(vm *goja.Runtime, raw string) (string, error) {
	if !strings.Contains(raw, "{{") || !strings.Contains(raw, "}}") {
		return raw, nil
	}
	var b strings.Builder
	rest := raw
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			b.WriteString(rest)
			return b.String(), nil
		}
		b.WriteString(rest[:start])
		rest = rest[start+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			b.WriteString("{{")
			b.WriteString(rest)
			return b.String(), nil
		}
		expr := strings.TrimSpace(rest[:end])
		value, err := vm.RunString(rewriteJavaCompatScript(expr))
		if err != nil {
			return "", r.wrapError(ErrScriptFailed, err)
		}
		b.WriteString(fmt.Sprint(value.Export()))
		rest = rest[end+2:]
	}
}

func looksLikeURL(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "/") || strings.Contains(value, "://")
}

type customURL struct {
	URL       string
	Attribute map[string]any
}

func parseCustomURL(raw string) customURL {
	raw = strings.TrimSpace(raw)
	out := customURL{URL: raw, Attribute: map[string]any{}}
	if raw == "" {
		return out
	}
	idx := strings.Index(raw, ",{")
	if idx < 0 {
		return out
	}
	out.URL = strings.TrimSpace(raw[:idx])
	_ = json.Unmarshal([]byte(raw[idx+1:]), &out.Attribute)
	return out
}

func parseHeaderJSONString(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(strings.ToLower(raw), "@js:") || strings.HasPrefix(strings.ToLower(raw), "<js>") {
		return map[string]string{}
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &out); err == nil {
		return out
	}
	for key, value := range urlrule.ParseAttributeObject(raw) {
		out[key] = fmt.Sprint(value)
	}
	return out
}

func applyURLOptions(req *fetch.Request, opts map[string]any) {
	var (
		charsetName string
		rawFormBody string
	)
	for key, value := range opts {
		switch strings.ToLower(key) {
		case "method":
			if method, ok := value.(string); ok && strings.TrimSpace(method) != "" {
				req.Method = strings.ToUpper(strings.TrimSpace(method))
			}
		case "body":
			if body, ok := value.(string); ok {
				rawFormBody = body
				if looksLikeFormBody(body) {
					req.Body = []byte(urlrule.EncodeFormString(body, ""))
				} else {
					req.Body = []byte(body)
				}
				if req.Headers["Content-Type"] == "" && looksLikeFormBody(body) {
					req.Headers["Content-Type"] = "application/x-www-form-urlencoded"
				}
			}
		case "headers":
			mergeHeaders(req.Headers, value)
		case "charset":
			if text, ok := value.(string); ok {
				charsetName = strings.TrimSpace(text)
			}
		case "retry":
			switch typed := value.(type) {
			case float64:
				if typed > 0 {
					req.Retry = int(typed)
				}
			case int:
				if typed > 0 {
					req.Retry = typed
				}
			case int64:
				if typed > 0 {
					req.Retry = int(typed)
				}
			case string:
				if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil && parsed > 0 {
					req.Retry = parsed
				}
			}
		}
	}
	req.URL = urlrule.EncodeURLQuery(req.URL, charsetName)
	if rawFormBody != "" && looksLikeFormBody(rawFormBody) {
		req.Body = []byte(urlrule.EncodeFormString(rawFormBody, charsetName))
	}
}

func looksLikeFormBody(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}
	if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") || strings.HasPrefix(body, "<") {
		return false
	}
	return strings.Contains(body, "=") || strings.Contains(body, "&")
}

func mergeHeaders(headers map[string]string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, raw := range typed {
			headers[key] = fmt.Sprint(raw)
		}
	case map[string]string:
		for key, raw := range typed {
			headers[key] = raw
		}
	case string:
		nested := map[string]string{}
		if err := json.Unmarshal([]byte(typed), &nested); err == nil {
			for key, text := range nested {
				headers[key] = text
			}
			return
		}
		for key, raw := range urlrule.ParseAttributeObject(typed) {
			headers[key] = fmt.Sprint(raw)
		}
	}
}

func normalizeScriptOptions(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case map[string]string:
		out := map[string]any{}
		for key, raw := range typed {
			out[key] = raw
		}
		return out
	case string:
		out := map[string]any{}
		if err := json.Unmarshal([]byte(typed), &out); err == nil {
			return out
		}
	}
	return map[string]any{}
}

var sourceCompatState sync.Map

type sourceCompatSnapshot struct {
	Variable    string
	LoginHeader string
	LoginInfo   string
}

func loadSourceCompatState(key string) sourceCompatSnapshot {
	if key == "" {
		return sourceCompatSnapshot{}
	}
	if value, ok := sourceCompatState.Load(key); ok {
		if state, ok := value.(sourceCompatSnapshot); ok {
			return state
		}
	}
	return sourceCompatSnapshot{}
}

func storeSourceCompatState(key string, state sourceCompatSnapshot) {
	if key == "" {
		return
	}
	sourceCompatState.Store(key, state)
}

func SourceBindings(src *domainsource.BookSource) map[string]any {
	if src == nil {
		return map[string]any{}
	}
	key := stripSourceSuffix(src.BookSourceURL)
	state := loadSourceCompatState(key)
	return map[string]any{
		"key":               key,
		"bookSourceUrl":     src.BookSourceURL,
		"bookSourceName":    src.BookSourceName,
		"bookSourceGroup":   src.BookSourceGroup,
		"bookSourceType":    src.BookSourceType,
		"bookSourceComment": src.BookSourceComment,
		"variableComment":   src.VariableComment,
		"loginUrl":          src.LoginURL,
		"header":            src.Header,
		"jsLib":             src.JSLib,
		"getKey": func() string {
			return key
		},
		"getLoginHeader": func() string {
			return loadSourceCompatState(key).LoginHeader
		},
		"getLoginHeaderMap": func() any {
			return compatLookupMap(loadSourceCompatState(key).LoginHeader)
		},
		"putLoginHeader": func(header string) bool {
			state := loadSourceCompatState(key)
			state.LoginHeader = header
			storeSourceCompatState(key, state)
			return true
		},
		"removeLoginHeader": func() bool {
			state := loadSourceCompatState(key)
			state.LoginHeader = ""
			storeSourceCompatState(key, state)
			return true
		},
		"getLoginInfo": func() any {
			info := loadSourceCompatState(key).LoginInfo
			if strings.TrimSpace(info) == "" {
				return nil
			}
			return info
		},
		"getLoginInfoMap": func() any {
			return compatLookupMap(loadSourceCompatState(key).LoginInfo)
		},
		"putLoginInfo": func(info string) bool {
			state := loadSourceCompatState(key)
			state.LoginInfo = info
			storeSourceCompatState(key, state)
			return true
		},
		"removeLoginInfo": func() bool {
			state := loadSourceCompatState(key)
			state.LoginInfo = ""
			storeSourceCompatState(key, state)
			return true
		},
		"getVariable": func(args ...any) string {
			return loadSourceCompatState(key).Variable
		},
		"setVariable": func(value string) bool {
			state := loadSourceCompatState(key)
			state.Variable = value
			storeSourceCompatState(key, state)
			return true
		},
		"putVariable": func(args ...any) bool {
			if len(args) == 1 {
				state := loadSourceCompatState(key)
				state.Variable = fmt.Sprint(args[0])
				storeSourceCompatState(key, state)
			}
			return true
		},
		"removeVariable": func() bool {
			state := loadSourceCompatState(key)
			state.Variable = ""
			storeSourceCompatState(key, state)
			return true
		},
		"variable": state.Variable,
	}
}

func compatLookupMap(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return emptyCompatLookupMap()
	}
	parsed := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return emptyCompatLookupMap()
	}
	out := map[string]any{}
	for key, value := range parsed {
		out[key] = value
	}
	out["get"] = func(key string) any {
		return parsed[key]
	}
	return out
}

func emptyCompatLookupMap() map[string]any {
	return map[string]any{
		"get": func(string) any {
			return nil
		},
	}
}

func hmacHex(data string, algorithm string, key string) string {
	normalized := strings.ToLower(strings.ReplaceAll(algorithm, "-", ""))
	var mac hashWriter
	switch normalized {
	case "hmacmd5", "md5":
		mac = hmac.New(md5.New, []byte(key))
	case "hmacsha1", "sha1":
		mac = hmac.New(sha1.New, []byte(key))
	case "hmacsha256", "sha256":
		mac = hmac.New(sha256.New, []byte(key))
	case "hmacsha384", "sha384":
		mac = hmac.New(sha512.New384, []byte(key))
	case "hmacsha512", "sha512":
		mac = hmac.New(sha512.New, []byte(key))
	default:
		mac = hmac.New(sha256.New, []byte(key))
	}
	_, _ = mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

type hashWriter interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

func randomUUIDString() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		now := time.Now().UnixNano()
		return fmt.Sprintf("00000000-0000-4000-8000-%012x", now&0xffffffffffff)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4],
		b[4:6],
		b[6:8],
		b[8:10],
		b[10:16],
	)
}

func coerceTimeValue(value any) (time.Time, bool) {
	switch v := value.(type) {
	case time.Time:
		return v, true
	case *time.Time:
		if v != nil {
			return *v, true
		}
	case int64:
		return unixGuess(v), true
	case int:
		return unixGuess(int64(v)), true
	case float64:
		return unixGuess(int64(v)), true
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return time.Time{}, false
		}
		if num, err := strconv.ParseInt(v, 10, 64); err == nil {
			return unixGuess(num), true
		}
	}
	return time.Time{}, false
}

func unixGuess(value int64) time.Time {
	if value > 1_000_000_000_000 {
		return time.UnixMilli(value)
	}
	return time.Unix(value, 0)
}

func javaTimeLayout(pattern string) string {
	replacer := strings.NewReplacer(
		"yyyy", "2006",
		"MM", "01",
		"dd", "02",
		"HH", "15",
		"hh", "03",
		"mm", "04",
		"ss", "05",
	)
	return replacer.Replace(pattern)
}

func javaStringCompat(vm *goja.Runtime, value string) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("length", len([]rune(value)))
	_ = obj.Set("valueOf", func() string {
		return value
	})
	_ = obj.Set("toString", func() *goja.Object {
		return javaStringCompat(vm, value)
	})
	_ = obj.Set("replace", func(oldValue any, newValue any) *goja.Object {
		return javaStringCompat(vm, strings.ReplaceAll(value, fmt.Sprint(oldValue), fmt.Sprint(newValue)))
	})
	_ = obj.Set("toLowerCase", func() string {
		return strings.ToLower(value)
	})
	_ = obj.Set("split", func(sep any) []string {
		return strings.Split(value, fmt.Sprint(sep))
	})
	return obj
}

func stripSourceSuffix(raw string) string {
	if i := strings.Index(raw, "##"); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.IndexAny(raw, "#＃"); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

func convertChinese(mode string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	switch mode {
	case "t2s":
		openccT2SOnce.Do(func() {
			openccT2S, openccT2SErr = opencc.New("t2s")
		})
		if openccT2SErr != nil || openccT2S == nil {
			return value
		}
		converted, err := openccT2S.Convert(value)
		if err != nil {
			return value
		}
		return converted
	case "s2t":
		openccS2TOnce.Do(func() {
			openccS2T, openccS2TErr = opencc.New("s2t")
		})
		if openccS2TErr != nil || openccS2T == nil {
			return value
		}
		converted, err := openccS2T.Convert(value)
		if err != nil {
			return value
		}
		return converted
	default:
		return value
	}
}

func aesBase64DecodeToString(data string, key string, transformation string, iv string) (string, error) {
	plain, err := decryptSymmetricBytes(decodeSymmetricInput(data), transformation, []byte(key), []byte(iv))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func desEncodeToBase64String(data string, key string, transformation string, iv string) (string, error) {
	cipherText, err := encryptSymmetricBytes([]byte(data), transformation, []byte(key), []byte(iv))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(cipherText), nil
}

func parseAESTransformation(transformation string) (mode string, padding string) {
	return parseSymmetricTransformation(transformation, "CBC", "PKCS5PADDING")
}

func parseSymmetricTransformation(transformation string, defaultMode string, defaultPadding string) (mode string, padding string) {
	parts := strings.Split(strings.ToUpper(strings.TrimSpace(transformation)), "/")
	mode = defaultMode
	padding = defaultPadding
	if len(parts) >= 2 && parts[1] != "" {
		mode = parts[1]
	}
	if len(parts) >= 3 && parts[2] != "" {
		padding = parts[2]
	}
	return mode, padding
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	if padding == 0 {
		padding = blockSize
	}
	out := make([]byte, 0, len(data)+padding)
	out = append(out, data...)
	for i := 0; i < padding; i++ {
		out = append(out, byte(padding))
	}
	return out
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("invalid padded data")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, fmt.Errorf("invalid padding")
	}
	for _, value := range data[len(data)-padding:] {
		if int(value) != padding {
			return nil, fmt.Errorf("invalid padding")
		}
	}
	return data[:len(data)-padding], nil
}

func (r *Runtime) installInterrupt(ctx context.Context, vm *goja.Runtime) func() {
	timeout := r.policy.Timeout
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			select {
			case <-done:
				return
			default:
				vm.Interrupt(ErrScriptTimeout)
			}
		case <-done:
		}
	}()
	return func() {
		close(done)
		cancel()
	}
}

func (r *Runtime) wrapError(kind error, err any) error {
	message := logging.RedactString(fmt.Sprint(err))
	if r.policy.Stage != "" {
		return fmt.Errorf("%w: %s: %s", kind, r.policy.Stage, message)
	}
	return fmt.Errorf("%w: %s", kind, message)
}

func (r *Runtime) Logs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.logs...)
}

type NamespacedCache struct {
	mu     sync.Mutex
	values map[string]map[string]any
}

func NewNamespacedCache() *NamespacedCache {
	return &NamespacedCache{values: map[string]map[string]any{}}
}

func (c *NamespacedCache) Put(namespace string, key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values[namespace] == nil {
		c.values[namespace] = map[string]any{}
	}
	c.values[namespace][key] = value
}

func (c *NamespacedCache) Get(namespace string, key string) any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.values[namespace][key]
}

type MemoryCookieStore struct {
	mu      sync.Mutex
	cookies map[string]string
	shared  *fetcherinfra.SessionCookieStore
}

func NewMemoryCookieStore(shared *fetcherinfra.SessionCookieStore) *MemoryCookieStore {
	return &MemoryCookieStore{cookies: map[string]string{}, shared: shared}
}

func (s *MemoryCookieStore) Bindings() map[string]any {
	return map[string]any{
		"setCookie":     s.SetCookie,
		"replaceCookie": s.ReplaceCookie,
		"getCookie":     s.GetCookie,
		"getKey":        s.GetKey,
		"removeCookie":  s.RemoveCookie,
		"cookieToMap":   CookieToMap,
		"mapToCookie":   MapToCookie,
	}
}

func (s *MemoryCookieStore) SetCookie(rawURL string, cookie string) {
	domain := cookieDomain(rawURL)
	if domain == "" {
		return
	}
	if s.shared != nil {
		s.shared.Save(domain, cookiesFromString(cookie))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cookies[domain] = cookie
}

func (s *MemoryCookieStore) ReplaceCookie(rawURL string, cookie string) {
	if strings.TrimSpace(cookie) == "" {
		return
	}
	domain := cookieDomain(rawURL)
	if domain == "" {
		return
	}
	if s.shared != nil {
		s.shared.Save(domain, cookiesFromString(cookie))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	merged := CookieToMap(s.cookies[domain])
	for key, value := range CookieToMap(cookie) {
		merged[key] = value
	}
	s.cookies[domain] = MapToCookie(merged)
}

func (s *MemoryCookieStore) GetCookie(rawURL string, keys ...string) string {
	if len(keys) > 0 && strings.TrimSpace(keys[0]) != "" {
		return s.GetKey(rawURL, keys[0])
	}
	domain := cookieDomain(rawURL)
	if domain == "" {
		return ""
	}
	s.mu.Lock()
	value := s.cookies[domain]
	s.mu.Unlock()
	if s.shared != nil {
		merged := CookieToMap(value)
		for key, item := range s.shared.List(domain) {
			merged[key] = item
		}
		return MapToCookie(merged)
	}
	return value
}

func (s *MemoryCookieStore) GetKey(rawURL string, key string) string {
	domain := cookieDomain(rawURL)
	if domain == "" {
		return ""
	}
	s.mu.Lock()
	value := CookieToMap(s.cookies[domain])[key]
	s.mu.Unlock()
	if value != "" {
		return value
	}
	if s.shared != nil {
		return s.shared.List(domain)[key]
	}
	return ""
}

func (s *MemoryCookieStore) RemoveCookie(rawURL string) {
	domain := cookieDomain(rawURL)
	if domain == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cookies, domain)
}

func cookiesFromString(raw string) []*http.Cookie {
	out := []*http.Cookie{}
	for key, value := range CookieToMap(raw) {
		out = append(out, &http.Cookie{Name: key, Value: value})
	}
	return out
}

func CookieToMap(cookie string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(cookie, ";") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		if value != "" || strings.EqualFold(value, "null") {
			out[key] = value
		}
	}
	return out
}

func MapToCookie(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := values[key]
		parts = append(parts, strings.TrimSpace(key)+"="+strings.TrimSpace(value))
	}
	return strings.Join(parts, "; ")
}

func cookieDomain(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	host := strings.TrimSpace(strings.ToLower(parsed.Hostname()))
	if domain, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil && strings.TrimSpace(domain) != "" {
		return domain
	}
	return strings.TrimPrefix(host, "www.")
}

func isForbiddenBinding(key string) bool {
	switch key {
	case "require", "process", "global", "globalThis", "window", "document", "XMLHttpRequest", "fetch":
		return true
	default:
		return false
	}
}

func validateRuntimeSupportedURLOptions(opts map[string]any, supportedKeys ...string) error {
	if len(opts) == 0 {
		return nil
	}
	allowed := map[string]struct{}{}
	for _, key := range supportedKeys {
		allowed[strings.ToLower(strings.TrimSpace(key))] = struct{}{}
	}
	for key, value := range opts {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, ok := allowed[normalized]; ok {
			continue
		}
		if isZeroLikeRuntimeOption(value) {
			continue
		}
		return fmt.Errorf("unsupported url option: %s", key)
	}
	return nil
}

func isZeroLikeRuntimeOption(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case bool:
		return !typed
	case float64:
		return typed == 0
	case int:
		return typed == 0
	case int64:
		return typed == 0
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}
