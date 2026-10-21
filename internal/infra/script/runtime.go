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
	"html"
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
	"github.com/dop251/goja/token"
	"golang.org/x/net/publicsuffix"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"

	domainbook "reader/internal/domain/book"
	"reader/internal/domain/fetch"
	domainsource "reader/internal/domain/source"
	"reader/internal/infra/chinese"
	fetcherinfra "reader/internal/infra/fetcher"
	"reader/internal/infra/logging"
	"reader/internal/infra/requestoption"
	ruleinfra "reader/internal/infra/rule"
	"reader/internal/infra/rule/jsonpath"
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

// maxAjaxTestAllConcurrency 是 java.ajaxTestAll 单次批量请求的最大并发度（S04-06）。
const maxAjaxTestAllConcurrency = 16

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
	// callSites 缓存热点脚本（主脚本、loginCheck、@js: header、模板表达式）的编译结果，
	// 与 jsLib 的 programs 分开，互不挤占容量。
	callSites *programCache
	logs      *logBuffer
	// ttfCache 按字体数据哈希缓存解析结果（S05-04）：*ttfFont 只读，可并发共享；容量有界。
	ttfCache *ttfCache
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
	runtime := &Runtime{
		policy:    policy,
		timeout:   timeout,
		cache:     cache,
		cookie:    NewMemoryCookieStore(policy.Cookie),
		variables: variables,
		files:     newFileSandbox(policy.FileRoot),
		programs:  newProgramCache(defaultProgramCacheSize),
		callSites: newProgramCache(defaultProgramCacheSize),
		logs:      newLogBuffer(defaultLogMaxEntries, defaultLogMaxBytes),
		ttfCache:  newTTFCache(defaultTTFCacheSize),
	}
	startMemoryWatchdog()
	return runtime
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

// EvalWithBindings 从 VM 池取 VM 执行脚本（超时过的 VM 直接丢弃不放回），bindings["source"]
// 可传 *BookSource，运行时会把它转换为白名单 source 绑定，原始指针不会暴露给脚本。
func (r *Runtime) EvalWithBindings(ctx context.Context, script string, bindings Bindings) (out any, err error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	// 每 Eval 新建 VM（方案 (a)）：goja 全局非严格模式声明不可配置（Delete 抛 TypeError），池化
	// 回收无法完整恢复原型链与 defineProperty 的非枚举属性，书源 jsLib 修改
	// String.prototype 等内置对象会跨源残留；放弃池化、只保留脚本编译缓存，语义与旧实现一致。
	vm := goja.New()
	// 一次 Eval 只注册一个中断回调：ctx 结束（超时或上层取消）即中断 VM。goja.Runtime 非并发安全：
	// defer stopInterrupt 与 defer cancel 同为 LIFO，stopInterrupt 后注册先执行，保证回调结束后
	// VM 才随本 Eval 丢弃，不会把 Interrupt 打到后续脚本。
	stopInterrupt := context.AfterFunc(ctx, func() { vm.Interrupt(ErrScriptTimeout) })
	defer stopInterrupt()
	// 内存看门狗（S04-03）：Eval 期间进程堆超过预算即中断本 VM，脚本以内存预算超限报错。
	unregisterVM := scriptMemoryWatchdog.trackVM(vm)
	defer unregisterVM()
	src := sourceFromBindings(r.policy.Source, bindings)
	runCtx := r.newEvalContext(ctx, src, bindings)
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok && errors.Is(recoveredErr, ErrScriptTimeout) {
				err = runCtx.wrap(ErrScriptTimeout, recovered)
			} else if recoveredErr, ok := recovered.(error); ok && errors.Is(recoveredErr, errScriptOutOfMemory) {
				err = runCtx.wrap(ErrScriptFailed, recoveredErr)
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
		// source 绑定只暴露白名单对象，原始 *BookSource 指针不进脚本（JsExtensions/BaseSource 对齐）。
		if key == "source" {
			if src, ok := value.(*domainsource.BookSource); ok {
				value = r.SourceBindings(runCtx.ctx, src)
			}
		}
		if err := vm.Set(key, prepareBindingValue(vm, key, value)); err != nil {
			return nil, runCtx.wrap(ErrScriptFailed, err)
		}
	}
	if err := r.loadSourceJSLib(vm, runCtx); err != nil {
		return nil, err
	}
	value, err := r.runScriptCached(vm, "eval.main", script)
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

// sourceFromBindings 解析书源对象：先取 policy 兜底，再取 Go 侧专用的 __source，最后取脚本可见的 source。
func sourceFromBindings(fallback *domainsource.BookSource, bindings Bindings) *domainsource.BookSource {
	if fallback != nil {
		return fallback
	}
	for _, key := range []string{"__source", "source"} {
		if src, ok := bindings[key].(*domainsource.BookSource); ok && src != nil {
			return src
		}
	}
	return nil
}

// prepareBindingValue 把 Go 侧 binding 转成脚本可见对象：响应、选择器、result 数组与 book/chapter 白名单。
func prepareBindingValue(vm *goja.Runtime, key string, value any) any {
	if resp, ok := value.(*fetch.Response); ok && resp != nil && key == "result" {
		return newScriptHTTPResponse(vm, resp, nil)
	}
	if sel, ok := value.(*goquery.Selection); ok && sel != nil {
		return newSelectionBinding(vm, sel)
	}
	if key == "result" {
		reflected := reflect.ValueOf(value)
		if reflected.IsValid() && (reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array) {
			if _, isBytes := value.([]byte); !isBytes {
				values := make([]any, reflected.Len())
				for i := range values {
					values[i] = reflected.Index(i).Interface()
				}
				return newElementCollection(vm, values)
			}
		}
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

// runScriptCached 在当前 VM 中执行热点脚本：编译结果按调用点 + 改写后文本哈希缓存（LRU）。
// *goja.Program 不可变，可在多个 VM 中并发运行；缓存命中路径不再重复编译与改写。
func (r *Runtime) runScriptCached(vm *goja.Runtime, site string, script string) (goja.Value, error) {
	program, err := r.callSites.compileScript(site, script)
	if err != nil {
		return nil, err
	}
	return vm.RunProgram(program)
}

var precompiledPrograms = struct {
	ajaxAll      *goja.Program
	stringProto  *goja.Program
	byteArrayPkg *goja.Program
}{
	ajaxAll: goja.MustCompile("", `
if (!java.ajaxAll) {
  java.ajaxAll = function(urls) {
    if (!urls || !urls.map) return [];
    return urls.map(function(u) { return java.connect(u); });
  };
}
`, false),
	stringProto: goja.MustCompile("", `
if (!String.prototype.getBytes) {
  Object.defineProperty(String.prototype, "getBytes", {
    value: function(charset){
      return __readerStringBytes(this.toString(), charset || "UTF-8");
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
`, false),
	byteArrayPkg: goja.MustCompile("", `
var __ReaderByteArrayInputStream = ByteArrayInputStream;
var __ReaderByteArrayOutputStream = ByteArrayOutputStream;
var __ReaderGZIPInputStream = GZIPInputStream;
ByteArrayInputStream = function(value){ return __ReaderByteArrayInputStream(value); };
ByteArrayOutputStream = function(){ return __ReaderByteArrayOutputStream(); };
GZIPInputStream = function(value){ return __ReaderGZIPInputStream(value); };
`, false),
}

func (r *Runtime) installSafeGlobals(vm *goja.Runtime, runCtx evalContext) error {
	for _, name := range []string{"require", "process", "global", "globalThis", "window", "document", "XMLHttpRequest", "fetch"} {
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
	if err := vm.Set("org", jsoupBindings(vm)); err != nil {
		return err
	}
	if _, err := vm.RunProgram(precompiledPrograms.ajaxAll); err != nil {
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
	// analyzerContent/analyzerBaseURL 记录 java.setContent 设置的待分析内容与基准地址，未设置时回落到 result 与 baseUrl 绑定。
	var analyzerContent any
	analyzerContentSet := false
	analyzerBaseURL := ""
	currentAnalyzerContent := func() any {
		if analyzerContentSet {
			return analyzerContent
		}
		value := vm.Get("result")
		if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
			return nil
		}
		return value.Export()
	}
	currentAnalyzerBaseURL := func() string {
		if analyzerBaseURL != "" {
			return analyzerBaseURL
		}
		return scriptAnalyzerBaseURL(vm)
	}
	var java map[string]any
	java = map[string]any{
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
		"getUserAgent": func() string {
			return sourceUserAgent(src)
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
		"digestHex": func(data any, algorithm any) string {
			return digestHex(fmt.Sprint(data), fmt.Sprint(algorithm))
		},
		"HMacHex": func(data any, algorithm any, key any) string {
			return hmacHex(fmt.Sprint(data), fmt.Sprint(algorithm), fmt.Sprint(key))
		},
		"HMacBase64": func(data any, algorithm any, key any) string {
			return base64.StdEncoding.EncodeToString(hmacBytes([]byte(fmt.Sprint(data)), fmt.Sprint(algorithm), []byte(fmt.Sprint(key))))
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
		"aesEncodeToBase64String": func(data any, key any, transformation any, iv any) string {
			encoded, err := aesEncodeToBase64String(fmt.Sprint(data), fmt.Sprint(key), fmt.Sprint(transformation), fmt.Sprint(iv))
			if err != nil {
				return ""
			}
			return encoded
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
		// timeFormatUTC(value, pattern, offset)：offset 是毫秒级的 UTC 时区偏移（Legado DateUtil 的 utc 偏移），
		// 与 yyyy 等模式一起按 UTC+offset 格式化。
		"timeFormatUTC": func(value any, pattern any, offsetMilliseconds any) string {
			t, ok := coerceTimeValue(value)
			if !ok {
				return ""
			}
			offset := int64(0)
			switch v := offsetMilliseconds.(type) {
			case int:
				offset = int64(v)
			case int64:
				offset = v
			case float64:
				offset = int64(v)
			case string:
				offset, _ = strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			}
			return t.UTC().Add(time.Duration(offset) * time.Millisecond).Format(javaTimeLayout(fmt.Sprint(pattern)))
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
		// setContent(html[, baseUrl])：设置待分析内容，返回 java 对象本身以便链式调用。
		"setContent": func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 || goja.IsUndefined(call.Argument(0)) || goja.IsNull(call.Argument(0)) {
				panic("content cannot be null")
			}
			analyzerContent = call.Argument(0).Export()
			analyzerContentSet = true
			if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
				analyzerBaseURL = call.Argument(1).String()
			}
			return vm.ToValue(java)
		},
		// head(url[, headers][, timeout])：第三个参数是本次请求的超时毫秒数（JsExtensions.kt）。
		"head": func(call goja.FunctionCall) goja.Value {
			callCtx, cancel := scriptCallContext(runCtx.ctx, call, 2)
			defer cancel()
			headCtx := runCtx
			headCtx.ctx = callCtx
			resp, err := r.fetchScriptCall(headCtx, vm, "HEAD", call)
			return newScriptHTTPResponse(vm, resp, err)
		},
		"post":    fetchResponse("POST"),
		"connect": fetchResponse("GET"),
		// getString(rule[, unescape|input][, absolute])：默认反转义 HTML 实体，与 Legado JsExtensions.getString 一致。
		"getString": func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				return vm.ToValue("")
			}
			input := currentAnalyzerContent()
			var fallback any
			if srcValue := vm.Get("src"); srcValue != nil && !goja.IsUndefined(srcValue) && !goja.IsNull(srcValue) {
				fallback = srcValue.Export()
			}
			unescape := true
			if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
				if enabled, ok := call.Argument(1).Export().(bool); ok {
					unescape = enabled
				} else {
					input = call.Argument(1).Export()
					fallback = nil
				}
			}
			value := r.scriptGetStringAt(runCtx.ctx, vm, input, fallback, call.Argument(0).String(), currentAnalyzerBaseURL())
			if unescape {
				value = html.UnescapeString(value)
			}
			if len(call.Arguments) > 2 && call.Argument(2).ToBoolean() {
				return vm.ToValue(urlrule.Absolute(currentAnalyzerBaseURL(), value))
			}
			return vm.ToValue(value)
		},
		// getStringList(rule[, input][, absolute])：第三个参数为 true 时按基准地址绝对化结果。
		"getStringList": func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				return goja.Null()
			}
			input := currentAnalyzerContent()
			if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
				input = call.Argument(1).Export()
			}
			values := r.scriptGetStringListAt(runCtx.ctx, vm, input, call.Argument(0).String(), currentAnalyzerBaseURL())
			if len(call.Arguments) > 2 && call.Argument(2).ToBoolean() {
				for i, value := range values {
					values[i] = urlrule.Absolute(currentAnalyzerBaseURL(), value)
				}
			}
			return vm.ToValue(values)
		},
		"getElements": func(rule any) goja.Value {
			return newElementCollection(vm, r.scriptGetElementsAt(runCtx.ctx, vm, currentAnalyzerContent(), fmt.Sprint(rule), currentAnalyzerBaseURL()))
		},
		"getElement": func(rule any) goja.Value {
			return r.scriptGetElementAt(runCtx.ctx, vm, currentAnalyzerContent(), fmt.Sprint(rule), currentAnalyzerBaseURL())
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
		// getResponse/getStrResponse 读取当前 VM 绑定的响应（__response），供 jsLib 复用上次请求结果。
		"getResponse": func() goja.Value {
			return newScriptHTTPResponse(vm, currentBoundResponse(vm), nil)
		},
		"getStrResponse": func(_ ...any) goja.Value {
			return newScriptHTTPResponse(vm, currentBoundResponse(vm), nil)
		},
		// ajaxTestAll(urls[, timeoutMillis][, skipRateLimit])：并发批量请求，返回响应数组（JsExtensions.kt:161）。
		// 以受限 errgroup 并发执行（并发度受 Policy.Timeout 总预算约束，单 URL ctx 仍是 runCtx 子集）。
		"ajaxTestAll": func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				return vm.NewArray()
			}
			urls := jsStringArray(vm, call.Argument(0))
			timeoutIndex := 1
			skipRate := len(call.Arguments) > 2 && call.Argument(2).ToBoolean()
			responses := make([]goja.Value, len(urls))
			if r.policy.Fetcher == nil {
				for i := range urls {
					responses[i] = newScriptHTTPResponse(vm, nil, ErrScriptFailed)
				}
				return vm.ToValue(responses)
			}
			var wg sync.WaitGroup
			type fetchResult struct {
				resp *fetch.Response
				err  error
			}
			results := make([]fetchResult, len(urls))
			// 并发度：与 Legado JsExtensions ajaxAll 一致全并发，但每个请求的 ctx 仍受 Eval 总超时约束。
			sem := make(chan struct{}, maxAjaxTestAllConcurrency)
			for i, rawURL := range urls {
				wg.Add(1)
				go func(i int, rawURL string) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					callCtx, cancel := scriptCallContext(runCtx.ctx, call, timeoutIndex)
					defer cancel()
					urlCtx := runCtx
					urlCtx.ctx = callCtx
					req, err := r.buildScriptRequest(urlCtx, vm, rawURL, nil)
					if err == nil {
						req.SkipRateLimit = skipRate
						results[i].resp, results[i].err = requestoption.Fetch(callCtx, r.policy.Fetcher, req, vmRequestEvaluator(r, vm))
					} else {
						results[i].err = err
					}
				}(i, rawURL)
			}
			wg.Wait()
			// goja.Runtime 非并发安全：响应包装回 VM goroutine 后再构造。
			for i := range results {
				responses[i] = newScriptHTTPResponse(vm, results[i].resp, results[i].err)
			}
			return vm.ToValue(responses)
		},
		"getCookie": func(call goja.FunctionCall) goja.Value {
			if key := optionalString(call, 1); key != "" {
				return vm.ToValue(r.cookie.GetKey(call.Argument(0).String(), key))
			}
			return vm.ToValue(r.cookie.GetCookie(call.Argument(0).String()))
		},
		"removeCookie": func(rawURL any) {
			r.cookie.RemoveCookie(fmt.Sprint(rawURL))
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
		// log(...args)：写入脚本日志；单参数时返回该参数以便脚本链式使用。
		"log": func(args ...any) any {
			r.log(fmt.Sprint(args...))
			if len(args) == 1 {
				return args[0]
			}
			return nil
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
		java[name] = unsupportedAPI(vm, name)
	}
	for name, fn := range r.fileBindings(runCtx, vm) {
		java[name] = fn
	}
	return java
}

func installJavaImporterCompat(vm *goja.Runtime) error {
	if err := vm.Set("__readerStringBytes", func(value any, charset ...any) []byte {
		charsetName := ""
		if len(charset) > 0 {
			charsetName = fmt.Sprint(charset[0])
		}
		return encodeStringBytes(fmt.Sprint(value), charsetName)
	}); err != nil {
		return err
	}
	if _, err := vm.RunProgram(precompiledPrograms.stringProto); err != nil {
		return err
	}
	if err := vm.Set("JavaImporter", func(call goja.ConstructorCall) *goja.Object {
		// 返回隔离对象而非 vm.GlobalObject()：全局对象本体不暴露给脚本（S05-06 防御纵深）。
		obj := vm.NewObject()
		proto := vm.NewObject()
		_ = proto.Set("importPackage", func(args ...any) {})
		_ = proto.Set("importClass", func(args ...any) {})
		_ = obj.SetPrototype(proto)
		return obj
	}); err != nil {
		return err
	}
	packages := vm.NewObject()
	javaPkg := vm.NewObject()
	javaLangPkg := vm.NewObject()
	javaIoPkg := vm.NewObject()
	javaNetPkg := vm.NewObject()
	javaSecurityPkg := vm.NewObject()
	javaSecurityInterfacesPkg := vm.NewObject()
	javaSecuritySpecPkg := vm.NewObject()
	javaUtilPkg := vm.NewObject()
	javaUtilUUIDPkg := vm.NewObject()
	javaUtilZipPkg := vm.NewObject()
	javaxPkg := vm.NewObject()
	javaxCryptoPkg := vm.NewObject()
	javaxCryptoSpecPkg := vm.NewObject()
	okhttpPkg := vm.NewObject()
	androidPkg := vm.NewObject()
	androidUtilPkg := vm.NewObject()
	androidOsPkg := vm.NewObject()
	androidBuildPkg := vm.NewObject()
	cnPkg := vm.NewObject()
	hutoolPkg := vm.NewObject()
	hutoolCorePkg := vm.NewObject()
	hutoolCoreUtilPkg := vm.NewObject()
	hutoolCoreCodecPkg := vm.NewObject()
	hutoolCryptoPkg := vm.NewObject()
	hutoolCryptoDigestPkg := vm.NewObject()
	hutoolCryptoSymmetricPkg := vm.NewObject()
	orgPkg := vm.NewObject()
	orgJsoupPkg := vm.NewObject()
	orgJsoupNodesPkg := vm.NewObject()

	javaStringConstructor := func(value any, charset ...any) *goja.Object {
		charsetName := ""
		if len(charset) > 0 {
			charsetName = fmt.Sprint(charset[0])
		}
		return javaStringCompat(vm, stringFromJavaValue(value, charsetName))
	}
	if err := javaLangPkg.Set("String", javaStringConstructor); err != nil {
		return err
	}
	urlEncoder := map[string]any{
		"encode": func(value any, charset ...any) string {
			charsetName := ""
			if len(charset) > 0 {
				charsetName = fmt.Sprint(charset[0])
			}
			return urlrule.EncodeURI(fmt.Sprint(value), charsetName)
		},
	}
	if err := javaNetPkg.Set("URLEncoder", urlEncoder); err != nil {
		return err
	}
	if err := javaSecurityPkg.Set("interfaces", javaSecurityInterfacesPkg); err != nil {
		return err
	}
	if err := javaSecurityPkg.Set("spec", javaSecuritySpecPkg); err != nil {
		return err
	}
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
	if err := javaPkg.Set("net", javaNetPkg); err != nil {
		return err
	}
	if err := javaPkg.Set("security", javaSecurityPkg); err != nil {
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
	if err := androidPkg.Set("util", androidUtilPkg); err != nil {
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
	if err := hutoolCryptoPkg.Set("symmetric", hutoolCryptoSymmetricPkg); err != nil {
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
	jsoupClass := jsoupClassBindings(vm)
	if err := orgJsoupPkg.Set("Jsoup", jsoupClass); err != nil {
		return err
	}
	if err := orgJsoupPkg.Set("nodes", orgJsoupNodesPkg); err != nil {
		return err
	}
	if err := orgPkg.Set("jsoup", orgJsoupPkg); err != nil {
		return err
	}
	if err := packages.Set("org", orgPkg); err != nil {
		return err
	}
	if err := vm.Set("Packages", packages); err != nil {
		return err
	}
	if err := vm.Set("android", androidPkg); err != nil {
		return err
	}
	if err := vm.Set("URLEncoder", urlEncoder); err != nil {
		return err
	}
	if err := vm.Set("Jsoup", jsoupClass); err != nil {
		return err
	}
	if err := vm.Set("importClass", func(args ...any) {}); err != nil {
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
	base64Compat := map[string]any{
		"DEFAULT":    0,
		"NO_PADDING": 1,
		"NO_WRAP":    2,
		"CRLF":       4,
		"URL_SAFE":   8,
		"encodeToString": func(value any, flags ...int) string {
			return androidBase64Encoding(firstInt(flags)).EncodeToString(bytesFromJS(value))
		},
		"decode": func(value any, flags ...int) []byte {
			encoding := androidBase64Encoding(firstInt(flags))
			decoded, err := encoding.DecodeString(strings.TrimSpace(string(bytesFromJS(value))))
			if err != nil {
				return decodeBase64Bytes(strings.TrimSpace(string(bytesFromJS(value))))
			}
			return decoded
		},
		"getDecoder": func() map[string]any {
			return map[string]any{
				"decode": func(value any) []byte {
					return decodeBase64Bytes(fmt.Sprint(value))
				},
			}
		},
	}
	if err := vm.Set("Base64", base64Compat); err != nil {
		return err
	}
	if err := androidUtilPkg.Set("Base64", base64Compat); err != nil {
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
	secretKeySpec := func(value any, algorithm any) map[string]any {
		return map[string]any{"key": bytesFromJS(value), "algorithm": fmt.Sprint(algorithm)}
	}
	if err := vm.Set("SecretKeySpec", secretKeySpec); err != nil {
		return err
	}
	if err := javaxCryptoSpecPkg.Set("SecretKeySpec", secretKeySpec); err != nil {
		return err
	}
	desKeySpec := func(value any) map[string]any {
		return map[string]any{"key": bytesFromJS(value), "algorithm": "DES"}
	}
	if err := vm.Set("DESKeySpec", desKeySpec); err != nil {
		return err
	}
	if err := javaxCryptoSpecPkg.Set("DESKeySpec", desKeySpec); err != nil {
		return err
	}
	ivParameterSpec := func(value any) map[string]any {
		return map[string]any{"iv": bytesFromJS(value)}
	}
	if err := vm.Set("IvParameterSpec", ivParameterSpec); err != nil {
		return err
	}
	if err := javaxCryptoSpecPkg.Set("IvParameterSpec", ivParameterSpec); err != nil {
		return err
	}
	secretKeyFactory := map[string]any{
		"getInstance": func(_ any) map[string]any {
			return map[string]any{"generateSecret": func(spec any) any { return spec }}
		},
	}
	if err := vm.Set("SecretKeyFactory", secretKeyFactory); err != nil {
		return err
	}
	if err := javaxCryptoPkg.Set("SecretKeyFactory", secretKeyFactory); err != nil {
		return err
	}
	macCompat := map[string]any{
		"getInstance": func(algorithm any) map[string]any {
			state := &javaMacCompat{algorithm: fmt.Sprint(algorithm)}
			return map[string]any{"init": state.init, "doFinal": state.doFinal}
		},
	}
	if err := vm.Set("Mac", macCompat); err != nil {
		return err
	}
	if err := javaxCryptoPkg.Set("Mac", macCompat); err != nil {
		return err
	}
	cipherCompat := map[string]any{
		"ENCRYPT_MODE": 1,
		"DECRYPT_MODE": 2,
		"getInstance": func(transformation any) map[string]any {
			state := &javaCipherCompat{transformation: fmt.Sprint(transformation)}
			return map[string]any{
				"init":    state.init,
				"doFinal": state.doFinal,
			}
		},
	}
	if err := vm.Set("Cipher", cipherCompat); err != nil {
		return err
	}
	if err := javaxCryptoPkg.Set("Cipher", cipherCompat); err != nil {
		return err
	}
	newHutoolCipher := func(algorithm string) func(any, any, any, ...any) map[string]any {
		return func(mode any, padding any, key any, iv ...any) map[string]any {
			var ivValue any
			if len(iv) > 0 {
				ivValue = iv[0]
			}
			transformation := fmt.Sprintf("%s/%s/%s", algorithm, mode, padding)
			return newSymmetricCryptoCompat(transformation, key, ivValue)
		}
	}
	if err := hutoolCryptoSymmetricPkg.Set("AES", newHutoolCipher("AES")); err != nil {
		return err
	}
	if err := hutoolCryptoSymmetricPkg.Set("DES", newHutoolCipher("DES")); err != nil {
		return err
	}
	if err := orgJsoupNodesPkg.Set("Document", vm.NewObject()); err != nil {
		return err
	}
	if err := orgJsoupNodesPkg.Set("Element", vm.NewObject()); err != nil {
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
	if _, err := vm.RunProgram(precompiledPrograms.byteArrayPkg); err != nil {
		return err
	}
	return nil
}

var rhinoLetConditionPattern = regexp.MustCompile(`\blet\s*\(\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*([^)]*?)\s*\)\s*([^)\r\n]+)`)

// rewriteJavaCompatScript 兼容 Rhino 语法差异。Rhino 会把 with(JavaImporter) 块内的函数声明提升到外层，
// goja 按块级作用域处理；因此只把顶层 with 块中直接声明的函数改写为赋值，其余代码（含注释、字符串）原样保留。
// Rhino 的 let(x=y) 条件式 let（goja 无法解析）在 AST 路径上先尝试：解析成功时按精确偏移改写，字符串
// 字面量中的 "let(a=1)" 形如文本不被误改写（S04-09）；解析失败（jsLib 含 Rhino 专属语法，goja 无法
// 解析做歧义判定）时回落到既有的行级正则改写，保持 Rhino jsLib 可编译。
func rewriteJavaCompatScript(script string) string {
	program, err := parser.ParseFile(nil, "", script, 0)
	if err != nil {
		// 解析失败：脚本含 Rhino 专属语法（如 let(x=y) 条件式 let），goja 无法做 AST 歧义判定。
		// 回落到行级正则改写（既有兼容行为），仅处理真正的 let(x=y) 形态；否则交给 goja 报真实语法错误。
		return rhinoLetConditionPattern.ReplaceAllString(script, `($1 = $2, $3)`)
	}
	type span struct {
		start, end  int
		replacement string
	}
	var spans []span
	for _, stmt := range program.Body {
		if lexical, ok := stmt.(*ast.LexicalDeclaration); ok && lexical.Token == token.LET {
			// Rhino 的 let(x=y) 条件式 let 在 goja AST 中解析为 LexicalDeclaration(LET)。
			// 顶层真正的 let 声明（let x = 1）同样是 LexicalDeclaration(LET)，但两个都不该改写：
			// 真正的 let 声明是合法 goja 语法；let(x=y) 形式在纯 goja 下会编译失败（Rhino 专属），
			// 因此仅当后续文本匹配 let(...) pattern 时按精确偏移改写。
			if len(lexical.List) == 1 {
				start := int(lexical.Idx0()) - 1
				end := int(lexical.Idx1()) - 1
				if start >= 0 && end <= len(script) && start < end {
					if replacement := rewriteRhinoLetSpan(script[start:end]); replacement != script[start:end] {
						spans = append(spans, span{start: start, end: end, replacement: replacement})
					}
				}
			}
		}
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
			nameStart := int(decl.Function.Function) - 1
			nameEnd := int(decl.Function.Name.Idx1()) - 1
			bodyEnd := int(decl.Function.Idx1()) - 1
			if nameStart < 0 || nameStart >= nameEnd || nameEnd > bodyEnd || bodyEnd > len(script) {
				return script
			}
			spans = append(spans, span{
				start:       nameStart,
				end:         bodyEnd,
				replacement: string(decl.Function.Name.Name) + " = function" + script[nameEnd:bodyEnd] + ";",
			})
		}
	}
	if len(spans) == 0 {
		return script
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var b strings.Builder
	b.Grow(len(script))
	last := 0
	for _, s := range spans {
		if s.start < last || s.end > len(script) {
			return script
		}
		b.WriteString(script[last:s.start])
		b.WriteString(s.replacement)
		last = s.end
	}
	b.WriteString(script[last:])
	return b.String()
}

// rewriteRhinoLetSpan 把 let(x=y) rest 片段改写为 (x = y, rest)（Rhino 条件式 let 语义）。
// 仅作用于 AST 确认的 LexicalDeclaration 偏移（S04-09），字符串字面量中的形如文本不会被改写。
func rewriteRhinoLetSpan(fragment string) string {
	match := rhinoLetConditionPattern.FindStringSubmatchIndex(fragment)
	if match == nil {
		return fragment
	}
	name := fragment[match[2]:match[3]]
	value := fragment[match[4]:match[5]]
	rest := fragment[match[6]:match[7]]
	return "(" + name + " = " + value + ", " + rest + ")"
}

type javaCipherCompat struct {
	transformation string
	mode           int
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
	// 走 requestoption.Fetch：bodyJs 链与 app 层请求保持同一套语义。
	return requestoption.Fetch(runCtx.ctx, r.policy.Fetcher, req, vmRequestEvaluator(r, vm))
}

func (r *Runtime) scriptGetStringAt(ctx context.Context, vm *goja.Runtime, input any, fallback any, rule string, baseURL string) string {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return ""
	}
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		BaseURL:     baseURL,
		RedirectURL: baseURL,
		SourceURL:   scriptAnalyzerSourceURL(vm),
		Script:      vmAnalyzerScript{r: r, vm: vm},
	})
	// 反转义由 JS 绑定层按 unescape 参数决定，这里取原始文本，避免双重反转义。
	if value, err := analyzer.GetStringRaw(ctx, rule, input); err == nil && value != "" {
		return value
	}
	if fallback != nil {
		if value, err := analyzer.GetStringRaw(ctx, rule, fallback); err == nil {
			return value
		}
	}
	return ""
}

func (r *Runtime) scriptGetStringListAt(ctx context.Context, vm *goja.Runtime, input any, rule string, baseURL string) []string {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return nil
	}
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		BaseURL:     baseURL,
		RedirectURL: baseURL,
		SourceURL:   scriptAnalyzerSourceURL(vm),
		Script:      vmAnalyzerScript{r: r, vm: vm},
	})
	values, err := analyzer.GetStringList(ctx, rule, input)
	if err != nil {
		return nil
	}
	return values
}

func (r *Runtime) scriptGetElementsAt(ctx context.Context, vm *goja.Runtime, input any, rule string, baseURL string) []any {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return nil
	}
	analyzer := ruleinfra.NewAnalyzer(ruleinfra.Context{
		BaseURL:     baseURL,
		RedirectURL: baseURL,
		SourceURL:   scriptAnalyzerSourceURL(vm),
		Script:      vmAnalyzerScript{r: r, vm: vm},
	})
	elements, err := analyzer.GetElements(ctx, rule, input)
	if err != nil {
		return nil
	}
	out := make([]any, 0)
	for _, element := range elements {
		if sel, ok := element.(*goquery.Selection); ok {
			out = append(out, newSelectionBinding(vm, sel))
			continue
		}
		out = append(out, element)
	}
	return out
}

// scriptGetElementAt 先按 JSONPath 取单个值，取不到时回落到元素集合（Legado getElement 语义）。
// 廉价预检：text[0] 不是 { 或 [ 时跳过 json.Valid 全文扫描（S05-05，HTML 正文每次 getElement
// 都免一次全文 JSON 验证）。
func (r *Runtime) scriptGetElementAt(ctx context.Context, vm *goja.Runtime, input any, rule string, baseURL string) goja.Value {
	text := strings.TrimSpace(scriptInputString(input))
	if text != "" && (text[0] == '{' || text[0] == '[') && json.Valid([]byte(text)) {
		if adapter, err := jsonpath.New(text); err == nil {
			if value, err := adapter.Object(normalizeScriptJSONRule(rule)); err == nil {
				return vm.ToValue(value)
			}
		}
	}
	return newElementCollection(vm, r.scriptGetElementsAt(ctx, vm, input, rule, baseURL))
}

// normalizeScriptJSONRule 把裸字段名/点号路径补成 JSONPath 规则（@Json: 前缀与 $ 前缀原样保留）。
func normalizeScriptJSONRule(rule string) string {
	rule = strings.TrimSpace(rule)
	if lower := strings.ToLower(rule); strings.HasPrefix(lower, "@json:") {
		rule = strings.TrimSpace(rule[len("@json:"):])
	}
	if rule == "" || strings.HasPrefix(rule, "$") || strings.HasPrefix(rule, "@") {
		if strings.HasPrefix(rule, "$.[") {
			return "$" + strings.TrimPrefix(rule, "$.")
		}
		return rule
	}
	if strings.HasPrefix(rule, ".[") {
		return "$" + strings.TrimPrefix(rule, ".")
	}
	if strings.HasPrefix(rule, ".") {
		return "$" + rule
	}
	return "$." + rule
}

// scriptInputString 把分析输入统一成文本：JSON 对象/数组序列化后再分析。
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

// vmAnalyzerScript 让规则分析器里的 @js 表达式在当前 VM 上求值，上下文与 java.* 绑定一致。
type vmAnalyzerScript struct {
	r  *Runtime
	vm *goja.Runtime
}

func (s vmAnalyzerScript) Eval(ctx context.Context, scriptText string, input any) (any, error) {
	return vmRequestEvaluator(s.r, s.vm)(ctx, scriptText, input)
}

// vmRequestEvaluator 用当前 VM 求值 URL 选项 js/bodyJs 等脚本表达式：求值期间把 result 绑定指向输入。
// 脚本表达式走按调用点 + 文本哈希的编译缓存（login_check 等 js 表达式高频重复）。
func vmRequestEvaluator(r *Runtime, vm *goja.Runtime) requestoption.Evaluator {
	return func(ctx context.Context, scriptText string, input any) (any, error) {
		previous := vm.Get("result")
		if previous == nil {
			previous = goja.Undefined()
		}
		if err := vm.Set("result", prepareBindingValue(vm, "result", input)); err != nil {
			return nil, err
		}
		defer func() { _ = vm.Set("result", previous) }()
		value, err := r.runScriptCached(vm, "requestoption.js", scriptText)
		if err != nil {
			return nil, err
		}
		return value.Export(), nil
	}
}

// currentBoundResponse 读取 app 层写入的 __response 绑定，供 java.getResponse/getStrResponse 复用。
func currentBoundResponse(vm *goja.Runtime) *fetch.Response {
	if vm == nil {
		return nil
	}
	value := vm.Get("__response")
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil
	}
	switch response := value.Export().(type) {
	case *fetch.Response:
		return response
	case fetch.Response:
		return &response
	default:
		return nil
	}
}

// newElementCollection 把元素列表包装成带 toArray/size/get/first 的 JS 集合（对齐 Legado Elements）。
func newElementCollection(vm *goja.Runtime, values []any) *goja.Object {
	normalized := make([]any, 0, len(values))
	for _, value := range values {
		if selection, ok := value.(*goquery.Selection); ok && selection != nil {
			normalized = append(normalized, newSelectionBinding(vm, selection))
			continue
		}
		normalized = append(normalized, value)
	}
	values = normalized
	collection := vm.NewArray()
	for i, value := range values {
		_ = collection.Set(strconv.Itoa(i), value)
	}
	defineMethod := func(name string, method any) {
		_ = collection.DefineDataProperty(name, vm.ToValue(method), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_FALSE)
	}
	defineMethod("toArray", func() *goja.Object { return collection })
	defineMethod("size", func() int { return len(values) })
	defineMethod("get", func(index int) goja.Value {
		if index < 0 || index >= len(values) {
			return goja.Undefined()
		}
		return collection.Get(strconv.Itoa(index))
	})
	defineMethod("first", func() goja.Value {
		if len(values) == 0 {
			return goja.Null()
		}
		return collection.Get("0")
	})
	defineMethod("text", func() string {
		parts := make([]string, 0, len(values))
		for _, value := range values {
			if selection := selectionFromBinding(value); selection != nil {
				if text := strings.TrimSpace(selection.Text()); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, " ")
	})
	defineMethod("attr", func(name string) string {
		for _, value := range values {
			if selection := selectionFromBinding(value); selection != nil {
				attribute, _ := selection.Attr(name)
				return attribute
			}
		}
		return ""
	})
	defineMethod("html", func() string {
		parts := make([]string, 0, len(values))
		for _, value := range values {
			if selection := selectionFromBinding(value); selection != nil {
				if content, err := selection.Html(); err == nil {
					parts = append(parts, content)
				}
			}
		}
		return strings.Join(parts, "\n")
	})
	defineMethod("select", func(selector string) *goja.Object {
		selected := make([]any, 0)
		for _, value := range values {
			selection := selectionFromBinding(value)
			if selection == nil {
				continue
			}
			selection.Find(selector).Each(func(_ int, item *goquery.Selection) {
				selected = append(selected, newSelectionBinding(vm, item))
			})
		}
		return newElementCollection(vm, selected)
	})
	defineMethod("selectFirst", func(selector string) goja.Value {
		selected := make([]any, 0, 1)
		for _, value := range values {
			selection := selectionFromBinding(value)
			if selection == nil {
				continue
			}
			if first := selection.Find(selector).First(); first.Length() > 0 {
				selected = append(selected, first)
				break
			}
		}
		if len(selected) == 0 {
			return goja.Null()
		}
		return vm.ToValue(newSelectionBinding(vm, selected[0].(*goquery.Selection)))
	})
	defineMethod("not", func(selector string) *goja.Object {
		filtered := make([]any, 0, len(values))
		for _, value := range values {
			selection := selectionFromBinding(value)
			if selection != nil && !selection.Is(selector) {
				filtered = append(filtered, value)
			}
		}
		return newElementCollection(vm, filtered)
	})
	return collection
}

// scriptCallContext 按调用的超时参数派生本次请求的 ctx（毫秒；未传或 <=0 时只做取消）。
func scriptCallContext(parent context.Context, call goja.FunctionCall, timeoutIndex int) (context.Context, context.CancelFunc) {
	if timeoutIndex >= 0 && len(call.Arguments) > timeoutIndex && !goja.IsUndefined(call.Argument(timeoutIndex)) && !goja.IsNull(call.Argument(timeoutIndex)) {
		milliseconds := call.Argument(timeoutIndex).ToInteger()
		if milliseconds > 0 {
			return context.WithTimeout(parent, time.Duration(milliseconds)*time.Millisecond)
		}
	}
	return context.WithCancel(parent)
}

// jsStringArray 把 JS 数组转成字符串切片。
func jsStringArray(vm *goja.Runtime, value goja.Value) []string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil
	}
	obj := value.ToObject(vm)
	if obj == nil {
		return nil
	}
	length := int(obj.Get("length").ToInteger())
	out := make([]string, 0, length)
	for i := range length {
		out = append(out, obj.Get(strconv.Itoa(i)).String())
	}
	return out
}

// sourceUserAgent 取书源 header 里的 User-Agent（@js 形式除外），缺省时用 Reader 默认 UA。
func sourceUserAgent(src *domainsource.BookSource) string {
	if src != nil {
		for key, value := range parseHeaderJSONString(src.Header) {
			if strings.EqualFold(key, "User-Agent") && strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return "Reader/0.1.0"
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

func newSelectionBinding(vm *goja.Runtime, sel *goquery.Selection) map[string]any {
	var binding map[string]any
	binding = map[string]any{
		"__selection": sel,
		"select": func(rule string) *goja.Object {
			values := make([]any, 0, sel.Find(rule).Length())
			sel.Find(rule).Each(func(_ int, item *goquery.Selection) {
				values = append(values, newSelectionBinding(vm, item))
			})
			return newElementCollection(vm, values)
		},
		"parentNode": func() map[string]any {
			return newSelectionBinding(vm, sel.Parent().First())
		},
		"previousElementSibling": func() any {
			previous := sel.Prev().First()
			if previous.Length() == 0 {
				return nil
			}
			return newSelectionBinding(vm, previous)
		},
		"first": func() map[string]any {
			return binding
		},
		"selectFirst": func(rule string) any {
			first := sel.Find(rule).First()
			if first.Length() == 0 {
				return nil
			}
			return newSelectionBinding(vm, first)
		},
		"hasClass": func(name string) bool {
			return sel.HasClass(name)
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
		"toString": func() string {
			value, _ := goquery.OuterHtml(sel)
			return value
		},
	}
	return binding
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
		// 脚本传入的 headers 走 S04-02 的保留键校验：Host/Connection/Content-Length 等保留键拒绝，
		// 防止书源作者伪造请求头。headers 键不做 URL 选项白名单校验（X-Test 等自定义头合法，
		// 与 Legado JsExtensions 一致），仅 rejectReservedHeaders。
		merged := map[string]any{}
		mergeAnyHeaders(merged, normalizeScriptOptions(call.Argument(headerIndex).Export()))
		if err := rejectReservedHeaders(merged, runCtx); err != nil {
			return nil, err
		}
		mergeHeaders(req.Headers, merged)
		// headers 里的 proxy 键是代理配置而非 HTTP 头（AnalyzeUrl.kt:128-131）。
		if proxy, ok := req.Headers["proxy"]; ok {
			delete(req.Headers, "proxy")
			req.Proxy = strings.TrimSpace(proxy)
		}
	}
	return requestoption.Fetch(runCtx.ctx, r.policy.Fetcher, req, vmRequestEvaluator(r, vm))
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
	_ = obj.Set("cookies", func() map[string]string {
		cookies := map[string]string{}
		if resp.Headers == nil {
			return cookies
		}
		for _, raw := range resp.Headers.Values("Set-Cookie") {
			cookie, parseErr := http.ParseSetCookie(raw)
			if parseErr == nil {
				cookies[cookie.Name] = cookie.Value
			}
		}
		return cookies
	})
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

// callableString 生成既能当字符串用（隐式转换）、又能像方法一样调用（`res.body()`）的值；
// 同时把 String.prototype 的方法转发到真实字符串，使 Legado 书源里 `result.body.replace(...)`
// 这类按属性直接调用字符串方法的写法同样可用。
func callableString(vm *goja.Runtime, value string) *goja.Object {
	fn, err := vm.RunString(`(function(v){
  var f = function(){ return v; };
  f.toString = function(){ return v; };
  f.valueOf = function(){ return v; };
  Object.getOwnPropertyNames(String.prototype).forEach(function(name){
    if (name === 'constructor' || typeof String.prototype[name] !== 'function') { return; }
    f[name] = function(){ return String.prototype[name].apply(v, arguments); };
  });
  return f;
})`)
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
	expanded, err := r.expandScriptTemplates(runCtx, vm, raw)
	if err != nil {
		return fetch.Request{}, err
	}
	if err := validateRuntimeSupportedURLOptions(urlrule.ParseCustom(expanded).Attribute, scriptSupportedURLOptions...); err != nil {
		return fetch.Request{}, runCtx.wrap(ErrScriptFailed, err)
	}
	baseURL := r.policy.BaseURL
	if baseURL == "" && src != nil {
		baseURL = stripSourceSuffix(src.BookSourceURL)
	}
	req := fetch.Request{Method: http.MethodGet, Headers: map[string]string{}}
	if src != nil {
		req.UseCookieJar = src.EnabledCookieJar
		req.SourceKey = stripSourceSuffix(src.BookSourceURL)
		req.ConcurrentRate = src.ConcurrentRate
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
	// URL 选项（js/bodyJs/retry/webView/proxy/dnsIp/body…）与 app 层共用 requestoption，语义不分叉。
	req, err = requestoption.Build(runCtx.ctx, expanded, baseURL, req, vmRequestEvaluator(r, vm))
	if err != nil {
		return fetch.Request{}, runCtx.wrap(ErrScriptFailed, err)
	}
	return req, nil
}

// scriptSupportedURLOptions 是脚本网络请求可识别的 URL 选项；serverID 仅用于 Legado WebDav，接受后忽略。
// webView 只作为 HTTP 回落使用，webJs/webViewDelayTime 需要真实 WebView，非零值时按不支持处理。
var scriptSupportedURLOptions = []string{"method", "body", "headers", "type", "charset", "origin", "retry", "proxy", "dnsIp", "serverID", "js", "bodyJs", "webView"}

func (r *Runtime) runtimeHeaderMap(runCtx evalContext, vm *goja.Runtime) (map[string]string, error) {
	src := runCtx.src
	header := strings.TrimSpace(src.Header)
	headers := map[string]string{}
	switch {
	case header == "":
	case strings.HasPrefix(strings.ToLower(header), "@js:"):
		value, err := r.runScriptCached(vm, "source.header.atjs", strings.TrimSpace(header[4:]))
		if err != nil {
			return nil, err
		}
		for key, headerValue := range parseHeaderJSONString(fmt.Sprint(value.Export())) {
			headers[key] = headerValue
		}
	case strings.HasPrefix(strings.ToLower(header), "<js>") && strings.HasSuffix(strings.ToLower(header), "</js>"):
		value, err := r.runScriptCached(vm, "source.header.tagjs", strings.TrimSpace(header[4:len(header)-5]))
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
	// 书源 header 的 proxy 键是网络代理配置而非 HTTP 头（AnalyzeUrl.kt:128-131）：空值丢弃，
	// 非空值需要代理客户端支持，脚本侧按不支持报错，避免被当成请求头发出去。
	for key, value := range headers {
		if !strings.EqualFold(strings.TrimSpace(key), "proxy") {
			continue
		}
		delete(headers, key)
		if strings.TrimSpace(value) != "" {
			return nil, fmt.Errorf("%w: source proxy is not supported", requestoption.ErrUnsupportedURLOption)
		}
	}
	return headers, nil
}

func (r *Runtime) expandScriptTemplates(runCtx evalContext, vm *goja.Runtime, raw string) (string, error) {
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
		value, err := r.runScriptCached(vm, "url.template", expr)
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
	// 独立 header JSON 的 loose 路径用 ParseHeaderObject：键保持原大小写（HTTP 头名敏感），
	// ParseAttributeObject 会把键归一为小写（仅用于 URL 选项）。
	for key, value := range urlrule.ParseHeaderObject(raw) {
		out[key] = fmt.Sprint(value)
	}
	return out
}

// mergeAnyHeaders 把任意形式的 headers 输入并进 map[string]any（不脱敏、不校验，仅供校验入口使用）。
func mergeAnyHeaders(target map[string]any, value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, raw := range typed {
			target[key] = raw
		}
	case map[string]string:
		for key, raw := range typed {
			target[key] = raw
		}
	case string:
		nested := map[string]string{}
		if err := json.Unmarshal([]byte(typed), &nested); err == nil {
			for key, text := range nested {
				target[key] = text
			}
			return
		}
		// headers 字符串是独立 header JSON：用 ParseHeaderObject 保持键原大小写。
		for key, raw := range urlrule.ParseHeaderObject(typed) {
			target[key] = raw
		}
	}
}

// rejectReservedHeaders 拒绝脚本 headers 中不可由用户代码设置的保留键（S04-02）。
func rejectReservedHeaders(headers map[string]any, runCtx evalContext) error {
	for key := range headers {
		if strings.EqualFold(strings.TrimSpace(key), "proxy") {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "host", "connection", "content-length", "transfer-encoding", "keep-alive":
			return runCtx.wrap(ErrScriptFailed, fmt.Errorf("%s header is reserved", key))
		}
	}
	return nil
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
		// headers 字符串是独立 header JSON：用 ParseHeaderObject 保持键原大小写；
		// ParseAttributeObject 归一为小写，仅用于 URL 选项解析。
		for key, raw := range urlrule.ParseHeaderObject(typed) {
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

// hmacHex 计算 HMAC 并返回十六进制摘要。
func hmacHex(data string, algorithm string, key string) string {
	return hex.EncodeToString(hmacBytes([]byte(data), algorithm, []byte(key)))
}

// hmacBytes 按算法名（HmacSHA256 / SHA-256 等写法均可）计算 HMAC，未知算法回落到 SHA-256。
func hmacBytes(data []byte, algorithm string, key []byte) []byte {
	normalized := strings.ToLower(strings.ReplaceAll(algorithm, "-", ""))
	var mac hashWriter
	switch normalized {
	case "hmacmd5", "md5":
		mac = hmac.New(md5.New, key)
	case "hmacsha1", "sha1":
		mac = hmac.New(sha1.New, key)
	case "hmacsha256", "sha256":
		mac = hmac.New(sha256.New, key)
	case "hmacsha384", "sha384":
		mac = hmac.New(sha512.New384, key)
	case "hmacsha512", "sha512":
		mac = hmac.New(sha512.New, key)
	default:
		mac = hmac.New(sha256.New, key)
	}
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

// digestHex 按摘要算法名（MD5/SHA-1/SHA-256 等）返回十六进制摘要，未知算法返回空串。
func digestHex(data string, algorithm string) string {
	normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToUpper(algorithm))
	switch normalized {
	case "MD5":
		sum := md5.Sum([]byte(data))
		return hex.EncodeToString(sum[:])
	case "SHA1":
		sum := sha1.Sum([]byte(data))
		return hex.EncodeToString(sum[:])
	case "SHA224":
		sum := sha256.Sum224([]byte(data))
		return hex.EncodeToString(sum[:])
	case "SHA256":
		sum := sha256.Sum256([]byte(data))
		return hex.EncodeToString(sum[:])
	case "SHA384":
		sum := sha512.Sum384([]byte(data))
		return hex.EncodeToString(sum[:])
	case "SHA512":
		sum := sha512.Sum512([]byte(data))
		return hex.EncodeToString(sum[:])
	default:
		return ""
	}
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
	_ = obj.Set("getBytes", func(charset ...any) []byte {
		charsetName := ""
		if len(charset) > 0 {
			charsetName = fmt.Sprint(charset[0])
		}
		return encodeStringBytes(value, charsetName)
	})
	_ = obj.Set("toLowerCase", func() string {
		return strings.ToLower(value)
	})
	_ = obj.Set("toUpperCase", func() *goja.Object {
		return javaStringCompat(vm, strings.ToUpper(value))
	})
	_ = obj.Set("trim", func() *goja.Object {
		return javaStringCompat(vm, strings.TrimSpace(value))
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

func aesEncodeToBase64String(data string, key string, transformation string, iv string) (string, error) {
	cipherText, err := encryptSymmetricBytes([]byte(data), transformation, []byte(key), []byte(iv))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(cipherText), nil
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

// reservedBindings 是脚本不可覆盖的绑定名单单一来源：installSafeGlobals 遮蔽的前两项与
// EvalWithBindings 的调用方 bindings 校验共用（S04-05，避免两处清单漂移）。
// 注意：只包含运行时自身安装的绑定（java/cache/cookie 等）与宿主遮蔽全局（require/process 等）；
// book/source/result/chapter/src/baseUrl 是调用方按 Legado 语义注入的合法 bindings，不禁（否则
// preUpdateJS、LoginCheckJS、@js ChapterURL 等链路全部断）。
var reservedBindings = []string{
	"require", "process", "global", "globalThis", "window", "document", "XMLHttpRequest", "fetch",
	// 运行时自身安装的绑定，调用方传入同键 bindings 会整体替换沙箱。
	"java", "cache", "cookie", "log", "org",
	"JavaImporter", "Packages", "Base64",
	"ByteArrayInputStream", "ByteArrayOutputStream", "GZIPInputStream", "__readerStringBytes",
}

func isReservedBinding(key string) bool {
	for _, name := range reservedBindings {
		if name == key {
			return true
		}
	}
	return false
}

func isForbiddenBinding(key string) bool {
	switch key {
	case "require", "process", "global", "globalThis", "window", "document", "XMLHttpRequest", "fetch":
		return true
	default:
		return isReservedBinding(key)
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

// ==== 合并自 origin/master 的兼容层辅助实现 ====

func androidBase64Encoding(flags int) *base64.Encoding {
	encoding := base64.StdEncoding
	if flags&8 != 0 {
		encoding = base64.URLEncoding
	}
	if flags&1 != 0 {
		encoding = encoding.WithPadding(base64.NoPadding)
	}
	return encoding
}

func chineseNumber(value string) (int, bool) {
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '壹': 1, '二': 2, '贰': 2, '两': 2, '三': 3, '叁': 3, '四': 4, '肆': 4, '五': 5, '伍': 5, '六': 6, '陆': 6, '七': 7, '柒': 7, '八': 8, '捌': 8, '九': 9, '玖': 9}
	units := map[rune]int{'十': 10, '拾': 10, '百': 100, '佰': 100, '千': 1000, '仟': 1000, '万': 10000, '亿': 100000000}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return 0, false
	}
	onlyDigits := true
	for _, r := range runes {
		if _, ok := units[r]; ok {
			onlyDigits = false
			break
		}
	}
	if onlyDigits {
		var b strings.Builder
		for _, r := range runes {
			switch {
			case r >= '0' && r <= '9':
				b.WriteRune(r)
			case r >= '０' && r <= '９':
				b.WriteRune('0' + (r - '０'))
			default:
				digit, ok := digits[r]
				if !ok {
					return 0, false
				}
				b.WriteByte(byte('0' + digit))
			}
		}
		parsed, err := strconv.Atoi(b.String())
		return parsed, err == nil
	}
	total, section, current := 0, 0, 0
	for _, r := range runes {
		if r >= '0' && r <= '9' {
			current = current*10 + int(r-'0')
			continue
		}
		if r >= '０' && r <= '９' {
			current = current*10 + int(r-'０')
			continue
		}
		if digit, ok := digits[r]; ok {
			current = digit
			continue
		}
		unit, ok := units[r]
		if !ok {
			return 0, false
		}
		if unit < 10000 {
			if current == 0 {
				current = 1
			}
			section += current * unit
		} else {
			section += current
			if section == 0 {
				section = 1
			}
			total += section * unit
			section = 0
		}
		current = 0
	}
	return total + section + current, true
}

func decodeBase64Bytes(value string) []byte {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded
		}
	}
	return nil
}

func decodeStringBytes(value any, charsetName string) string {
	data := bytesFromJS(value)
	charsetName = normalizeCharsetName(charsetName)
	if strings.EqualFold(charsetName, "UTF-8") || strings.EqualFold(charsetName, "UTF8") {
		return string(data)
	}
	encoding, err := htmlindex.Get(charsetName)
	if err != nil || encoding == nil {
		return string(data)
	}
	decoded, _, err := transform.Bytes(encoding.NewDecoder(), data)
	if err != nil {
		return string(data)
	}
	return string(decoded)
}

func encodeStringBytes(value string, charsetName string) []byte {
	charsetName = normalizeCharsetName(charsetName)
	if strings.EqualFold(charsetName, "UTF-8") || strings.EqualFold(charsetName, "UTF8") {
		return []byte(value)
	}
	encoding, err := htmlindex.Get(charsetName)
	if err != nil || encoding == nil {
		return []byte(value)
	}
	encoded, _, err := transform.Bytes(encoding.NewEncoder(), []byte(value))
	if err != nil {
		return []byte(value)
	}
	return encoded
}

func firstInt(values []int) int {
	if len(values) == 0 {
		return 0
	}
	return values[0]
}

func jsoupBindings(vm *goja.Runtime) map[string]any {
	return map[string]any{
		"jsoup": map[string]any{
			"Jsoup": jsoupClassBindings(vm),
		},
	}
}

func jsoupClassBindings(vm *goja.Runtime) map[string]any {
	parse := func(raw any) (*goja.Object, error) {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(fmt.Sprint(raw)))
		if err != nil {
			return nil, err
		}
		return newElementCollection(vm, []any{newSelectionBinding(vm, doc.Selection)}), nil
	}
	return map[string]any{
		"parse": parse,
	}
}

func normalizeCharsetName(charsetName string) string {
	charsetName = strings.TrimSpace(strings.ReplaceAll(charsetName, "_", "-"))
	if charsetName == "" {
		return "UTF-8"
	}
	return charsetName
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

func selectionFromBinding(value any) *goquery.Selection {
	if selection, ok := value.(*goquery.Selection); ok {
		return selection
	}
	binding, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	selection, _ := binding["__selection"].(*goquery.Selection)
	return selection
}

func stringFromJavaValue(value any, charsetName string) string {
	if text, ok := value.(string); ok {
		return text
	}
	return decodeStringBytes(value, charsetName)
}

var chapterNumberPattern = regexp.MustCompile(`第([^章]+)章`)

func toNumericChapter(value string) string {
	match := chapterNumberPattern.FindStringSubmatchIndex(value)
	if len(match) != 4 {
		return value
	}
	number, ok := chineseNumber(value[match[2]:match[3]])
	if !ok {
		return value
	}
	return value[:match[2]] + strconv.Itoa(number) + value[match[3]:]
}

func (s *javaByteArrayOutputStream) ToString() string {
	return s.buf.String()
}

type javaMacCompat struct {
	algorithm string
	key       []byte
}

// ==== 合并自 origin/master 的兼容层辅助实现 ====

func (m *javaMacCompat) init(keySpec any) {
	m.key = bytesFromJS(keySpec)
}

func (m *javaMacCompat) doFinal(value any) []byte {
	return hmacBytes(bytesFromJS(value), m.algorithm, m.key)
}

func (c *javaCipherCompat) init(mode int, keySpec any, ivSpec ...any) {
	c.mode = mode
	c.key = bytesFromJS(keySpec)
	if len(ivSpec) > 0 {
		c.iv = bytesFromJS(ivSpec[0])
	}
}

func (c *javaCipherCompat) doFinal(value any) any {
	if c.mode == 1 {
		encoded, err := encryptSymmetricBytes(bytesFromJS(value), c.transformation, c.key, c.iv)
		if err != nil {
			return []byte(nil)
		}
		return encoded
	}
	decoded, err := decryptSymmetricBytes(bytesFromJS(value), c.transformation, c.key, c.iv)
	if err != nil {
		return ""
	}
	return string(decoded)
}
