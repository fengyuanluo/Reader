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
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
	"golang.org/x/net/publicsuffix"

	domainbook "reader/internal/domain/book"
	"reader/internal/domain/fetch"
	domainsource "reader/internal/domain/source"
	"reader/internal/infra/chinese"
	fetcherinfra "reader/internal/infra/fetcher"
	"reader/internal/infra/logging"
	ruleinfra "reader/internal/infra/rule"
	urlrule "reader/internal/infra/rule/url"
)

var (
	ErrScriptFailed  = errors.New("script_failed")
	ErrScriptTimeout = errors.New("script_timeout")
	ErrUnsafeGlobal  = errors.New("unsafe global access")
	// ErrScriptUnsupported 表示服务端无法提供的 Legado 能力（WebView、人工交互、7z/rar 等）。
	ErrScriptUnsupported = errors.New("script_unsupported")
)

const defaultWebViewUserAgent = "Mozilla/5.0 (Linux; Android 10; K; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/122.0.0.0 Mobile Safari/537.36"

// defaultScriptTimeout 是 Policy.Timeout<=0 时的执行上限，保证脚本与其网络请求总有截止时间。
var defaultScriptTimeout = 30 * time.Second

type Bindings map[string]any

// ChapterTitleBinding 返回 Legado AnalyzeRule.kt:785 顶层 title 绑定的取值（chapter?.title）：
// 正文、目录、详情各脚本适配器共用它，无章节上下文时与 Legado 一致为 null。
func ChapterTitleBinding(chapter *domainbook.BookChapter) any {
	if chapter == nil {
		return nil
	}
	return chapter.Title
}

type Policy struct {
	AllowLog  bool
	Timeout   time.Duration
	SourceKey string
	BaseURL   string
	Fetcher   fetch.Fetcher
	Source    *domainsource.BookSource
	Cookie    *fetcherinfra.SessionCookieStore
	Cache     *NamespacedCache
	// Variables 持久化书源级状态（variable/loginHeader/loginInfo/source.put）；为空时使用 Runtime 私有内存实现。
	Variables domainsource.VariableRepository
	// FileRoot 是 java.readFile/downloadFile 等本地文件 API 的沙箱根目录；为空时这些 API 返回 ErrScriptUnsupported。
	FileRoot string
}

// Runtime 可被多个服务并发共享：所有可变状态在 NewRuntime 中一次性初始化，并各自带锁。
type Runtime struct {
	policy    Policy
	timeout   time.Duration
	cache     *NamespacedCache
	cookie    *MemoryCookieStore
	variables domainsource.VariableRepository
	files     *fileSandbox
	programs  *programCache
	logs      *logBuffer
}

// evalContext 是单次 Eval 的只读上下文，不写回共享 Runtime。
type evalContext struct {
	ctx       context.Context
	stage     string
	sourceKey string
	sourceID  string
	src       *domainsource.BookSource
}

func NewRuntime(policy Policy) *Runtime {
	timeout := policy.Timeout
	if timeout <= 0 {
		timeout = defaultScriptTimeout
	}
	cache := policy.Cache
	if cache == nil {
		cache = NewNamespacedCache()
	}
	variables := policy.Variables
	if variables == nil {
		variables = newMemoryVariableRepository()
	}
	return &Runtime{
		policy:    policy,
		timeout:   timeout,
		cache:     cache,
		cookie:    NewMemoryCookieStore(policy.Cookie),
		variables: variables,
		files:     newFileSandbox(policy.FileRoot),
		programs:  newProgramCache(defaultProgramCacheSize),
		logs:      newLogBuffer(defaultLogMaxEntries, defaultLogMaxBytes),
	}
}

type stageContextKey struct{}

// WithStage 为后续脚本执行标注阶段（search、toc.format_js 等），用于错误信息与调试事件。
func WithStage(ctx context.Context, stage string) context.Context {
	return context.WithValue(ctx, stageContextKey{}, stage)
}

// WithSubStage 在已有阶段后追加子阶段，例如 search → search.header_js。
func WithSubStage(ctx context.Context, sub string) context.Context {
	if parent := StageFromContext(ctx); parent != "" {
		return WithStage(ctx, parent+"."+sub)
	}
	return WithStage(ctx, sub)
}

// StageFromContext 返回 WithStage 设置的阶段；未设置时为空。
func StageFromContext(ctx context.Context) string {
	stage, _ := ctx.Value(stageContextKey{}).(string)
	return stage
}

func (r *Runtime) Eval(ctx context.Context, script string, input any) (out any, err error) {
	return r.EvalWithBindings(ctx, script, Bindings{"result": input})
}

// EvalWithBindings 每次新建独立 goja VM 执行脚本。bindings["source"] 可传 *BookSource，
// 运行时会把它转换为白名单 source 绑定，原始指针不会暴露给脚本。
func (r *Runtime) EvalWithBindings(ctx context.Context, script string, bindings Bindings) (out any, err error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	vm := goja.New()
	// 一次 Eval 只注册一个中断回调：ctx 结束（超时或上层取消）即中断 VM，jsLib 与主脚本共用。
	stopInterrupt := context.AfterFunc(ctx, func() { vm.Interrupt(ErrScriptTimeout) })
	defer stopInterrupt()
	src := sourceFromBindings(r.policy.Source, bindings)
	runCtx := r.newEvalContext(ctx, src, bindings)
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok && errors.Is(recoveredErr, ErrScriptTimeout) {
				err = runCtx.wrap(ErrScriptTimeout, recovered)
			} else {
				err = runCtx.wrap(ErrScriptFailed, recovered)
			}
			out = nil
		}
	}()
	// 安装全局对象也会执行 JS，可能被超时中断，需与主脚本一样区分超时。
	if err := r.installSafeGlobals(vm, runCtx); err != nil {
		return nil, runCtx.wrapRunError(err)
	}
	for key, value := range bindings {
		if isForbiddenBinding(key) {
			return nil, fmt.Errorf("%w: %s", ErrUnsafeGlobal, key)
		}
		if err := vm.Set(key, r.prepareBindingValue(runCtx, key, value)); err != nil {
			return nil, runCtx.wrap(ErrScriptFailed, err)
		}
	}
	if err := r.loadSourceJSLib(vm, runCtx); err != nil {
		return nil, err
	}
	value, err := vm.RunString(rewriteJavaCompatScript(script))
	if err != nil {
		return nil, runCtx.wrapRunError(err)
	}
	// goja 只在跳转处检查中断：脚本在网络请求被取消后可能直接跑完，此时结果基于失败的请求，按超时/取消报错。
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, runCtx.wrap(ErrScriptTimeout, ctxErr)
	}
	return value.Export(), nil
}

func (r *Runtime) newEvalContext(ctx context.Context, src *domainsource.BookSource, bindings Bindings) evalContext {
	runCtx := evalContext{ctx: ctx, stage: StageFromContext(ctx), sourceKey: r.sourceKeyForBindings(src, bindings), src: src}
	if src != nil {
		runCtx.sourceID = src.SourceID
	}
	if runCtx.sourceID == "" {
		runCtx.sourceID = runCtx.sourceKey
	}
	return runCtx
}

// wrapRunError 区分超时中断与普通脚本错误。
func (c evalContext) wrapRunError(err error) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) || strings.Contains(err.Error(), ErrScriptTimeout.Error()) {
		return c.wrap(ErrScriptTimeout, err)
	}
	return c.wrap(ErrScriptFailed, err)
}

// wrap 生成 "<kind>: stage=<stage> source=<sourceId>: <message>"，保留 kind 前缀供诊断分类。
func (c evalContext) wrap(kind error, err any) error {
	message := logging.RedactString(fmt.Sprint(err))
	stage := c.stage
	if stage == "" {
		stage = "unknown"
	}
	if c.sourceID != "" {
		return fmt.Errorf("%w: stage=%s source=%s: %s", kind, stage, c.sourceID, message)
	}
	return fmt.Errorf("%w: stage=%s: %s", kind, stage, message)
}

func (r *Runtime) sourceKeyForBindings(src *domainsource.BookSource, bindings Bindings) string {
	if key := strings.TrimSpace(r.policy.SourceKey); key != "" {
		return stripSourceSuffix(key)
	}
	return stripSourceSuffix(sourceKeyFromBindings(src, bindings))
}

func sourceFromBindings(fallback *domainsource.BookSource, bindings Bindings) *domainsource.BookSource {
	if fallback != nil {
		return fallback
	}
	src, _ := bindings["source"].(*domainsource.BookSource)
	return src
}

func (r *Runtime) prepareBindingValue(runCtx evalContext, key string, value any) any {
	if sel, ok := value.(*goquery.Selection); ok && sel != nil {
		return newSelectionBinding(sel)
	}
	switch key {
	case "source":
		if src, ok := value.(*domainsource.BookSource); ok {
			return r.SourceBindings(runCtx.ctx, src)
		}
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

// loadSourceJSLib 在当前 VM 中执行书源 jsLib。编译结果按内容哈希缓存，VM 仍每次独立，状态不跨 Eval 共享。
func (r *Runtime) loadSourceJSLib(vm *goja.Runtime, runCtx evalContext) error {
	src := runCtx.src
	if src == nil || strings.TrimSpace(src.JSLib) == "" {
		return nil
	}
	program, err := r.programs.compile(src.JSLib)
	if err != nil {
		return runCtx.wrap(ErrScriptFailed, err)
	}
	if _, err := vm.RunProgram(program); err != nil {
		return runCtx.wrapRunError(err)
	}
	return nil
}

func (r *Runtime) installSafeGlobals(vm *goja.Runtime, runCtx evalContext) error {
	blocked := []string{"require", "process", "global", "globalThis", "window", "document", "XMLHttpRequest", "fetch"}
	for _, name := range blocked {
		if err := vm.Set(name, goja.Undefined()); err != nil {
			return err
		}
	}
	if r.policy.AllowLog {
		if err := vm.Set("log", func(args ...any) {
			r.log(fmt.Sprint(args...))
		}); err != nil {
			return err
		}
	}
	if err := vm.Set("cache", r.cacheBindings(runCtx)); err != nil {
		return err
	}
	if err := vm.Set("java", r.javaBindings(vm, runCtx)); err != nil {
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
	return vm.Set("cookie", r.cookie.Bindings())
}

// log 在 AllowLog 开启时写入有界日志缓冲（已脱敏）。
func (r *Runtime) log(message string) {
	if r.policy.AllowLog {
		r.logs.Append(message)
	}
}

// cacheBindings 对齐 Legado CacheManager（put 的第三个参数为保存秒数），按书源 key 隔离且有界。
func (r *Runtime) cacheBindings(runCtx evalContext) map[string]any {
	put := func(key string, value any, saveTime ...int64) {
		var ttl time.Duration
		if len(saveTime) > 0 && saveTime[0] > 0 {
			ttl = time.Duration(saveTime[0]) * time.Second
		}
		r.cache.PutTTL(runCtx.sourceKey, key, cacheValueString(value), ttl)
	}
	get := func(key string) any {
		value, ok := r.cache.Get(runCtx.sourceKey, key)
		if !ok {
			return nil
		}
		return value
	}
	remove := func(key string) {
		r.cache.Delete(runCtx.sourceKey, key)
	}
	return map[string]any{
		"get":           get,
		"put":           put,
		"delete":        remove,
		"getFromMemory": get,
		"putMemory":     func(key string, value any) { put(key, value) },
		"deleteMemory":  remove,
	}
}

// throwJS 以脚本可 try/catch 的异常形式抛出 Go 错误。
func throwJS(vm *goja.Runtime, err error) {
	panic(vm.NewGoError(err))
}

func unsupportedAPI(vm *goja.Runtime, name string) func(goja.FunctionCall) goja.Value {
	return func(goja.FunctionCall) goja.Value {
		throwJS(vm, fmt.Errorf("%w: java.%s requires an interactive browser or device", ErrScriptUnsupported, name))
		return goja.Undefined()
	}
}

func optionalString(call goja.FunctionCall, index int) string {
	if len(call.Arguments) <= index {
		return ""
	}
	value := call.Argument(index)
	if goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	return value.String()
}

func (r *Runtime) javaBindings(vm *goja.Runtime, runCtx evalContext) map[string]any {
	src := runCtx.src
	fetchResponse := func(method string) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			resp, err := r.fetchScriptCall(runCtx, vm, method, call)
			return newScriptHTTPResponse(vm, resp, err)
		}
	}
	bindings := map[string]any{
		"getSource": func() any {
			if src == nil {
				return nil
			}
			return r.SourceBindings(runCtx.ctx, src)
		},
		"base64Encode": func(call goja.FunctionCall) goja.Value {
			flags := base64FlagNoWrap
			if len(call.Arguments) > 1 {
				flags = int(call.Argument(1).ToInteger())
			}
			return vm.ToValue(base64EncodeFlags([]byte(call.Argument(0).String()), flags))
		},
		"getWebViewUA": func() string {
			return defaultWebViewUserAgent
		},
		"t2s": func(value any) string {
			return chinese.T2S(fmt.Sprint(value))
		},
		"s2t": func(value any) string {
			return chinese.S2T(fmt.Sprint(value))
		},
		// base64Decode(str[, charset|flags])：第二参数为字符串时按字符集解码，为数字时是 Android flags（解码已兼容各变体）。
		"base64Decode": func(call goja.FunctionCall) goja.Value {
			decoded := base64DecodeLenient(optionalString(call, 0))
			charsetName := ""
			if len(call.Arguments) > 1 {
				if text, ok := call.Argument(1).Export().(string); ok {
					charsetName = text
				}
			}
			text, err := decodeCharset(decoded, charsetName)
			if err != nil {
				throwJS(vm, err)
			}
			return vm.ToValue(text)
		},
		"base64DecodeToByteArray": func(call goja.FunctionCall) goja.Value {
			text := optionalString(call, 0)
			if strings.TrimSpace(text) == "" {
				return goja.Null()
			}
			return vm.ToValue(base64DecodeLenient(text))
		},
		"md5Encode": func(value any) string {
			sum := md5.Sum([]byte(fmt.Sprint(value)))
			return hex.EncodeToString(sum[:])
		},
		"md5Encode16": func(value any) string {
			return md5Encode16(fmt.Sprint(value))
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
		"hexDecodeToByteArray": func(value any) goja.Value {
			decoded, err := hex.DecodeString(fmt.Sprint(value))
			if err != nil {
				return goja.Null()
			}
			return vm.ToValue(decoded)
		},
		"hexEncodeToString": func(value any) string {
			return hex.EncodeToString([]byte(fmt.Sprint(value)))
		},
		"strToBytes": func(call goja.FunctionCall) goja.Value {
			encoded, err := encodeCharset(call.Argument(0).String(), optionalString(call, 1))
			if err != nil {
				throwJS(vm, err)
			}
			return vm.ToValue(encoded)
		},
		"bytesToStr": func(call goja.FunctionCall) goja.Value {
			text, err := decodeCharset(bytesFromJS(call.Argument(0)), optionalString(call, 1))
			if err != nil {
				throwJS(vm, err)
			}
			return vm.ToValue(text)
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
		"androidId": func() string {
			return readerAndroidID
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
		"timeFormat": func(value any) string {
			return timeFormat(value)
		},
		"htmlFormat": func(value any) string {
			return htmlFormatKeepImg(fmt.Sprint(value))
		},
		"toNumChapter": func(value goja.Value) goja.Value {
			if goja.IsUndefined(value) || goja.IsNull(value) {
				return goja.Null()
			}
			return vm.ToValue(toNumChapter(value.String()))
		},
		"toURL": func(call goja.FunctionCall) goja.Value {
			parsed, err := newJSURL(call.Argument(0).String(), optionalString(call, 1))
			if err != nil {
				throwJS(vm, err)
			}
			return vm.ToValue(parsed)
		},
		"toast":     func(msg any) { r.log(fmt.Sprint(msg)) },
		"longToast": func(msg any) { r.log(fmt.Sprint(msg)) },
		"put": func(key string, value any) any {
			r.cache.Put(runCtx.sourceKey, key, cacheValueString(value))
			return value
		},
		// get(key) 只读规则变量与缓存（BaseSource.kt:232）；get(url, headers) 才发起请求（JsExtensions.kt:378）。
		"get": func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				return goja.Undefined()
			}
			if len(call.Arguments) == 1 {
				key := call.Argument(0).String()
				if value, ok := getRuntimeVariable(vm, key); ok {
					return vm.ToValue(value)
				}
				if value, ok := r.cache.Get(runCtx.sourceKey, key); ok {
					return vm.ToValue(value)
				}
				return vm.ToValue("")
			}
			return fetchResponse("GET")(call)
		},
		"head":    fetchResponse("HEAD"),
		"post":    fetchResponse("POST"),
		"connect": fetchResponse("GET"),
		"getString": func(rule any) string {
			var fallback any
			if srcValue := vm.Get("src"); srcValue != nil && !goja.IsUndefined(srcValue) && !goja.IsNull(srcValue) {
				fallback = srcValue.Export()
			}
			return scriptGetString(runCtx.ctx, vm, vm.Get("result").Export(), fallback, fmt.Sprint(rule))
		},
		"getElements": func(rule any) []any {
			return scriptGetElements(runCtx.ctx, vm, vm.Get("result").Export(), fmt.Sprint(rule))
		},
		// ajax 失败时返回错误文本，与 Legado JsExtensions.kt:93-111 的 getOrElse { stackTraceStr } 一致。
		"ajax": func(raw goja.Value) string {
			target := raw.Export()
			if list, ok := target.([]any); ok {
				target = ""
				if len(list) > 0 {
					target = list[0]
				}
			}
			resp, err := r.fetchScriptURL(runCtx, vm, fmt.Sprint(target), nil)
			if err != nil {
				return err.Error()
			}
			return string(resp.Body)
		},
		"getCookie": func(call goja.FunctionCall) goja.Value {
			if key := optionalString(call, 1); key != "" {
				return vm.ToValue(r.cookie.GetKey(call.Argument(0).String(), key))
			}
			return vm.ToValue(r.cookie.GetCookie(call.Argument(0).String()))
		},
		"cacheFile": func(call goja.FunctionCall) goja.Value {
			var saveTime int64
			if len(call.Arguments) > 1 {
				saveTime = call.Argument(1).ToInteger()
			}
			text, err := r.cacheFile(runCtx, vm, call.Argument(0).String(), saveTime)
			if err != nil {
				throwJS(vm, err)
			}
			return vm.ToValue(text)
		},
		// importScript 对齐 JsExtensions.kt:264：http 开头走 cacheFile，否则读沙箱内文本文件。
		"importScript": func(path string) string {
			var (
				text string
				err  error
			)
			if strings.HasPrefix(path, "http") {
				text, err = r.cacheFile(runCtx, vm, path, 0)
			} else {
				text, err = r.files.readText(path, "")
			}
			if err != nil {
				throwJS(vm, err)
			}
			if strings.TrimSpace(text) == "" {
				throwJS(vm, fmt.Errorf("%w: %s 内容获取失败或者为空", ErrScriptFailed, path))
			}
			return text
		},
		"log": func(msg any) any {
			r.log(fmt.Sprint(msg))
			return msg
		},
		"logType": func(value goja.Value) {
			if goja.IsUndefined(value) || goja.IsNull(value) {
				r.log("null")
				return
			}
			r.log(fmt.Sprintf("%T", value.Export()))
		},
		"queryTTF": func(call goja.FunctionCall) goja.Value {
			return r.queryTTFValue(runCtx, vm, call.Argument(0))
		},
		"queryBase64TTF": func(call goja.FunctionCall) goja.Value {
			return r.queryTTFValue(runCtx, vm, call.Argument(0))
		},
		"replaceFont": func(call goja.FunctionCall) goja.Value {
			filter := len(call.Arguments) > 3 && call.Argument(3).ToBoolean()
			return vm.ToValue(replaceFont(call.Argument(0).String(), exportedTTF(call.Argument(1)), exportedTTF(call.Argument(2)), filter))
		},
	}
	for _, name := range []string{"webView", "webViewGetSource", "webViewGetOverrideUrl", "startBrowser", "startBrowserAwait", "getVerificationCode", "openUrl"} {
		bindings[name] = unsupportedAPI(vm, name)
	}
	for name, fn := range r.fileBindings(runCtx, vm) {
		bindings[name] = fn
	}
	return bindings
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

var rhinoLetConditionPattern = regexp.MustCompile(`\blet\s*\(\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*([^)]*?)\s*\)\s*([^)\r\n]+)`)

// rewriteJavaCompatScript 兼容 Rhino 语法差异。Rhino 会把 with(JavaImporter) 块内的函数声明提升到外层，
// goja 按块级作用域处理；因此只把顶层 with 块中直接声明的函数改写为赋值，其余代码（含注释、字符串）原样保留。
func rewriteJavaCompatScript(script string) string {
	script = rhinoLetConditionPattern.ReplaceAllString(script, `($1 = $2, $3)`)
	if !strings.Contains(script, "with") {
		return script
	}
	program, err := parser.ParseFile(nil, "", script, 0)
	if err != nil {
		// 解析失败时不做猜测性改写，交给 goja 报出真实语法错误。
		return script
	}
	type span struct {
		start, nameEnd, bodyEnd int
		name                    string
	}
	var spans []span
	for _, stmt := range program.Body {
		with, ok := stmt.(*ast.WithStatement)
		if !ok {
			continue
		}
		block, ok := with.Body.(*ast.BlockStatement)
		if !ok {
			continue
		}
		for _, inner := range block.List {
			decl, ok := inner.(*ast.FunctionDeclaration)
			if !ok || decl.Function == nil || decl.Function.Name == nil {
				continue
			}
			// file.Idx 从 1 开始，转换为字节偏移。
			spans = append(spans, span{
				start:   int(decl.Function.Function) - 1,
				nameEnd: int(decl.Function.Name.Idx1()) - 1,
				bodyEnd: int(decl.Function.Idx1()) - 1,
				name:    string(decl.Function.Name.Name),
			})
		}
	}
	if len(spans) == 0 {
		return script
	}
	var b strings.Builder
	b.Grow(len(script) + len(spans)*9)
	last := 0
	for _, s := range spans {
		if s.start < last || s.start >= s.nameEnd || s.nameEnd > s.bodyEnd || s.bodyEnd > len(script) {
			return script
		}
		b.WriteString(script[last:s.start])
		b.WriteString(s.name)
		b.WriteString(" = function")
		// 赋值表达式需要显式结束，避免同一行相邻的两个声明拼接成语法错误。
		b.WriteString(script[s.nameEnd:s.bodyEnd])
		b.WriteString(";")
		last = s.bodyEnd
	}
	b.WriteString(script[last:])
	return b.String()
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

// fetchScriptURL 发起脚本侧请求；ctx 取自本次 Eval，受 Policy.Timeout 与上层取消控制。
func (r *Runtime) fetchScriptURL(runCtx evalContext, vm *goja.Runtime, raw string, extraHeaders map[string]string) (*fetch.Response, error) {
	if r.policy.Fetcher == nil {
		return nil, runCtx.wrap(ErrScriptFailed, "fetcher is not configured")
	}
	req, err := r.buildScriptRequest(runCtx, vm, raw, extraHeaders)
	if err != nil {
		return nil, err
	}
	return r.policy.Fetcher.Fetch(runCtx.ctx, req)
}

func scriptGetString(ctx context.Context, vm *goja.Runtime, input any, fallback any, rule string) string {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return ""
	}
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		RedirectURL: scriptAnalyzerBaseURL(vm),
		SourceURL:   scriptAnalyzerSourceURL(vm),
	})
	if value, err := analyzer.GetString(ctx, rule, input); err == nil && value != "" {
		return value
	}
	if fallback != nil {
		if value, err := analyzer.GetString(ctx, rule, fallback); err == nil {
			return value
		}
	}
	return ""
}

func scriptGetElements(ctx context.Context, vm *goja.Runtime, input any, rule string) []any {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return nil
	}
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		RedirectURL: scriptAnalyzerBaseURL(vm),
		SourceURL:   scriptAnalyzerSourceURL(vm),
	})
	elements, err := analyzer.GetElements(ctx, rule, input)
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
	// book/chapter 绑定的 getVariable 读取实时值，避免 putVariable 之后读到绑定创建时的 variable 快照。
	if getter, ok := raw["getVariable"].(func(string) string); ok {
		if value := getter(key); value != "" {
			return value, true
		}
		return nil, false
	}
	variableRaw, ok := raw["variable"].(string)
	if !ok {
		return nil, false
	}
	value, ok = decodeVariableMap(variableRaw)[key]
	return value, ok
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
	value, ok := decodeVariableMap(field.String())[key]
	return value, ok
}

// decodeVariableMap 对齐 Legado RuleData.variableMap（Book.kt:137-139）：非 JSON 或空字符串视为空表。
func decodeVariableMap(raw string) map[string]any {
	vars := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return vars
	}
	if err := json.Unmarshal([]byte(raw), &vars); err != nil || vars == nil {
		return map[string]any{}
	}
	return vars
}

// compatGetVariable 对齐 RuleData.getVariable(key)：缺键返回空串；非字符串值按 JSON 返回。
func compatGetVariable(raw *string, key string) string {
	if raw == nil {
		return ""
	}
	return variableValueString(decodeVariableMap(*raw)[key])
}

func variableValueString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
}

// compatPutVariable 对齐 RuleData.putVariable(key, value)：value 为 null/undefined 时删除该键。
func compatPutVariable(raw *string, key string, value any) string {
	if raw == nil {
		return variableValueString(value)
	}
	vars := decodeVariableMap(*raw)
	if value == nil {
		delete(vars, key)
	} else {
		vars[key] = variableValueString(value)
	}
	if encoded, err := json.Marshal(vars); err == nil {
		*raw = string(encoded)
	}
	return variableValueString(value)
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

// fetchScriptCall 实现 java.get/post/head/connect(url[, body][, headers])；post 的对象 body 序列化为 JSON。
func (r *Runtime) fetchScriptCall(runCtx evalContext, vm *goja.Runtime, method string, call goja.FunctionCall) (*fetch.Response, error) {
	if r.policy.Fetcher == nil {
		return nil, runCtx.wrap(ErrScriptFailed, "fetcher is not configured")
	}
	req, err := r.buildScriptRequest(runCtx, vm, call.Argument(0).String(), nil)
	if err != nil {
		return nil, err
	}
	present := func(index int) bool {
		return len(call.Arguments) > index && !goja.IsUndefined(call.Argument(index)) && !goja.IsNull(call.Argument(index))
	}
	headerIndex := 1
	if method != "GET" {
		req.Method = method
	}
	if method == "POST" {
		headerIndex = 2
		if present(1) {
			switch body := call.Argument(1).Export().(type) {
			case map[string]any, []any:
				encoded, err := json.Marshal(body)
				if err != nil {
					return nil, runCtx.wrap(ErrScriptFailed, err)
				}
				req.Body = encoded
				if req.Headers["Content-Type"] == "" {
					req.Headers["Content-Type"] = "application/json"
				}
			default:
				req.Body = []byte(call.Argument(1).String())
			}
		}
	}
	if present(headerIndex) {
		mergeHeaders(req.Headers, normalizeScriptOptions(call.Argument(headerIndex).Export()))
		if proxy, ok := req.Headers["proxy"]; ok {
			delete(req.Headers, "proxy")
			req.Proxy = strings.TrimSpace(proxy)
		}
	}
	return r.policy.Fetcher.Fetch(runCtx.ctx, req)
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
		resp = &fetch.Response{StatusCode: 0, Headers: fetch.Headers{}, Body: []byte(errorText)}
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

func callableHeaders(vm *goja.Runtime, headers fetch.Headers) *goja.Object {
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

func (r *Runtime) buildScriptRequest(runCtx evalContext, vm *goja.Runtime, raw string, extraHeaders map[string]string) (fetch.Request, error) {
	src := runCtx.src
	expanded, err := expandScriptTemplates(runCtx, vm, raw)
	if err != nil {
		return fetch.Request{}, err
	}
	custom := parseCustomURL(expanded)
	if err := validateRuntimeSupportedURLOptions(custom.Attribute, scriptSupportedURLOptions...); err != nil {
		return fetch.Request{}, runCtx.wrap(ErrScriptFailed, err)
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
		headers, err := r.runtimeHeaderMap(runCtx, vm)
		if err != nil {
			return fetch.Request{}, runCtx.wrap(ErrScriptFailed, err)
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

// scriptSupportedURLOptions 是脚本网络请求可识别的 URL 选项；serverID 仅用于 Legado WebDav，接受后忽略。
var scriptSupportedURLOptions = []string{"method", "body", "headers", "type", "charset", "origin", "retry", "proxy", "dnsIp", "serverID"}

func (r *Runtime) runtimeHeaderMap(runCtx evalContext, vm *goja.Runtime) (map[string]string, error) {
	src := runCtx.src
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
	loginHeader, _, err := r.variables.GetVariable(runCtx.ctx, src.BookSourceURL, domainsource.VariableNameLoginHeader)
	if err != nil {
		return nil, err
	}
	for key, value := range parseHeaderJSONString(loginHeader) {
		headers[key] = value
	}
	return headers, nil
}

func expandScriptTemplates(runCtx evalContext, vm *goja.Runtime, raw string) (string, error) {
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
			return "", runCtx.wrapRunError(err)
		}
		b.WriteString(fmt.Sprint(value.Export()))
		rest = rest[end+2:]
	}
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

// applyURLOptions 对齐 AnalyzeUrl.UrlOption：body 可为字符串或对象/数组（序列化为 JSON），
// type 非空时响应按 hex 返回，origin 写入 Origin/Referer；proxy/dnsIp 交给 fetcher 处理。
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
			switch body := value.(type) {
			case string:
				rawFormBody = body
				if urlrule.LooksLikeFormBody(body) {
					req.Body = []byte(urlrule.EncodeFormString(body, ""))
					if req.Headers["Content-Type"] == "" {
						req.Headers["Content-Type"] = "application/x-www-form-urlencoded"
					}
				} else {
					req.Body = []byte(body)
				}
			case map[string]any, []any:
				if encoded, err := json.Marshal(body); err == nil {
					req.Body = encoded
					if req.Headers["Content-Type"] == "" {
						req.Headers["Content-Type"] = "application/json"
					}
				}
			}
		case "headers":
			mergeHeaders(req.Headers, value)
		case "type":
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				req.EncodeBodyAsHex = true
			}
		case "origin":
			if origin, ok := value.(string); ok && strings.TrimSpace(origin) != "" {
				req.Headers["Origin"] = strings.TrimSpace(origin)
				if req.Headers["Referer"] == "" {
					req.Headers["Referer"] = strings.TrimSpace(origin)
				}
			}
		case "charset":
			if text, ok := value.(string); ok {
				charsetName = strings.TrimSpace(text)
			}
		case "proxy":
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				req.Proxy = strings.TrimSpace(text)
			}
		case "dnsip":
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				req.DNSIP = strings.TrimSpace(text)
			}
		case "retry":
			urlrule.ApplyRetryOption(req, value)
		}
	}
	// 书源 header 中的 proxy 键是代理配置而非 HTTP 头（AnalyzeUrl.kt:128-131）；URL 选项 proxy 优先。
	if proxy, ok := req.Headers["proxy"]; ok {
		delete(req.Headers, "proxy")
		if req.Proxy == "" {
			req.Proxy = strings.TrimSpace(proxy)
		}
	}
	req.URL = urlrule.EncodeURLQuery(req.URL, charsetName)
	if rawFormBody != "" && urlrule.LooksLikeFormBody(rawFormBody) {
		req.Body = []byte(urlrule.EncodeFormString(rawFormBody, charsetName))
	}
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

// Logs 返回有界日志缓冲的快照（仅 AllowLog 时写入）。
func (r *Runtime) Logs() []string {
	return r.logs.Snapshot()
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
