package requestoption

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"reader/internal/domain/fetch"
	urlrule "reader/internal/infra/rule/url"
)

var (
	ErrUnsupportedURLOption = errors.New("unsupported url option")
	ErrScriptUnavailable    = errors.New("script runtime is not configured")
)

type Evaluator func(context.Context, string, any) (any, error)

func Build(ctx context.Context, rawURL string, baseURL string, base fetch.Request, eval Evaluator) (fetch.Request, error) {
	custom := urlrule.ParseCustom(rawURL)
	if err := Validate(custom.Attribute); err != nil {
		return fetch.Request{}, err
	}
	base.Headers = cloneHeaders(base.Headers)
	if base.Method == "" {
		base.Method = http.MethodGet
	}
	base.URL = urlrule.Absolute(baseURL, custom.URL)
	if base.URL == "" {
		base.URL = custom.URL
	}

	if value, ok := optionValue(custom.Attribute, "method"); ok {
		if method := strings.TrimSpace(fmt.Sprint(value)); method != "" {
			base.Method = strings.ToUpper(method)
		}
	}
	if value, ok := optionValue(custom.Attribute, "headers"); ok {
		mergeHeaders(base.Headers, value)
	}
	// 对齐 Legado AnalyzeUrl：type 键存在且值为非空白非 null 字符串时按 hex 二进制处理响应。
	// Legado setType 将 isNullOrBlank 的 type 归为 null（AnalyzeUrl.kt:746-759），
	// 空白串（如 `{"type":""}`）与 null 视为未设置，不触发 hex 编码（S07-06/S09-04）。
	if value, ok := optionValue(custom.Attribute, "type"); ok && value != nil {
		if typeText := strings.TrimSpace(fmt.Sprint(value)); typeText != "" && typeText != "<nil>" {
			base.EncodeBodyAsHex = true
		}
	}
	if value, ok := optionValue(custom.Attribute, "origin"); ok {
		if origin := strings.TrimSpace(fmt.Sprint(value)); origin != "" {
			setHeader(base.Headers, "Origin", origin)
			if headerValue(base.Headers, "Referer") == "" {
				setHeader(base.Headers, "Referer", origin)
			}
		}
	}
	charsetName := ""
	if value, ok := optionValue(custom.Attribute, "charset"); ok {
		charsetName = strings.TrimSpace(fmt.Sprint(value))
	}
	if value, ok := optionValue(custom.Attribute, "retry"); ok {
		base.Retry = positiveInt(value)
	}
	if value, ok := optionValue(custom.Attribute, "bodyjs"); ok {
		base.BodyJS = strings.TrimSpace(fmt.Sprint(value))
	}
	if value, ok := optionValue(custom.Attribute, "webjs"); ok {
		webJs := strings.TrimSpace(fmt.Sprint(value))
		if webJs != "" {
			// 对齐 Legado：webJs 只在 WebView 生效（AnalyzeUrl.kt getStrResponse 的 useWebView 分支）。
			// 未声明 webView 时 webJs 完全不执行，丢弃 BodyJS（S07-09）。
			if webView, ok := optionValue(custom.Attribute, "webview"); ok && optionTruthy(webView) {
				base.BodyJS = webJs
				base.WebViewFallback = true
			}
		}
	}
	if value, ok := optionValue(custom.Attribute, "webview"); ok {
		base.WebViewFallback = optionTruthy(value)
	}
	rawFormBody := ""
	if value, ok := optionValue(custom.Attribute, "body"); ok {
		rawFormBody = applyBody(&base, value)
	}
	if scriptText := optionString(custom.Attribute, "js"); scriptText != "" {
		if eval == nil {
			return fetch.Request{}, ErrScriptUnavailable
		}
		value, err := eval(ctx, scriptText, base.URL)
		if err != nil {
			return fetch.Request{}, err
		}
		if value != nil {
			base.URL = fmt.Sprint(value)
		}
	}
	base.URL = urlrule.EncodeURLQuery(base.URL, charsetName)
	if rawFormBody != "" {
		base.Body = []byte(urlrule.EncodeFormString(rawFormBody, charsetName))
	}
	ApplyNetworkOptions(&base, custom.Attribute)
	return base, nil
}

func Fetch(ctx context.Context, fetcher fetch.Fetcher, req fetch.Request, eval Evaluator) (*fetch.Response, error) {
	resp, err := fetcher.Fetch(ctx, req)
	// webJs→BodyJS 的写入已被 Build 的 webView 门限制（S07-09：非 WebView 不生效）；
	// bodyJs 是 Reader 独立扩展选项（docs/Design.md/Requirement.md 的"响应体后处理 JS"），
	// 与 WebViewFallback 无关，这里只按 BodyJS 是否非空判断。
	if err != nil || strings.TrimSpace(req.BodyJS) == "" {
		return resp, err
	}
	if eval == nil {
		return nil, ErrScriptUnavailable
	}
	value, err := eval(ctx, req.BodyJS, string(resp.Body))
	if err != nil {
		return nil, err
	}
	next := *resp
	if value == nil {
		next.Body = []byte("null")
	} else {
		next.Body = []byte(fmt.Sprint(value))
	}
	return &next, nil
}

// supportedOptions 是 Reader 识别的 URL 选项（Legado AnalyzeUrl.UrlOption），按小写比较。
// proxy / dnsIp 生效；serverID 只用于 Legado 的 WebDav 备份，webJs / webViewDelayTime 只在 WebView
// 生效时有意义，三者在此接受但不产生效果。其余选项在零值（null / 空串 / false / 0 / 空容器）时放行。
var supportedOptions = map[string]struct{}{
	"body": {}, "bodyjs": {}, "charset": {}, "dnsip": {}, "headers": {}, "js": {},
	"method": {}, "origin": {}, "proxy": {}, "retry": {}, "serverid": {}, "type": {},
	"webjs": {}, "webview": {}, "webviewdelaytime": {},
}

// Validate 拒绝 Reader 不支持的 URL 选项：未知选项仅在非零值时按 ErrUnsupportedURLOption 报错。
func Validate(opts map[string]any) error {
	for key, value := range opts {
		if _, ok := supportedOptions[strings.ToLower(strings.TrimSpace(key))]; ok {
			continue
		}
		if zeroLike(value) {
			continue
		}
		return fmt.Errorf("%w: %s", ErrUnsupportedURLOption, key)
	}
	return nil
}

// SanitizeSourceHeaders 处理书源请求头里的 proxy 键：按 Legado AnalyzeUrl 约定（AnalyzeUrl.kt:128-131）
// 它是代理配置而非 HTTP 头，零值直接丢弃；非空值保留在 headers 中，由 Build / ApplyNetworkOptions
// 构建请求时移出为 fetch.Request.Proxy，避免被当成请求头发送。
func SanitizeSourceHeaders(headers map[string]string) error {
	for key, value := range headers {
		if strings.EqualFold(strings.TrimSpace(key), "proxy") && zeroLike(value) {
			delete(headers, key)
		}
	}
	return nil
}

// ApplyNetworkOptions 写入请求级网络固定配置：请求头里遗留的 proxy 键（Legado 约定的代理配置）
// 移出请求头作为代理，URL 选项 proxy / dnsIp 覆盖之（对齐 AnalyzeUrl.kt:113,263-265 getProxyClient）。
func ApplyNetworkOptions(req *fetch.Request, opts map[string]any) {
	if req == nil {
		return
	}
	if proxy, ok := extractHeaderProxy(req.Headers); ok {
		req.Proxy = strings.TrimSpace(proxy)
	}
	for key, value := range opts {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "proxy":
			req.Proxy = strings.TrimSpace(text)
		case "dnsip":
			req.DNSIP = strings.TrimSpace(text)
		}
	}
}

// extractHeaderProxy 移出请求头里的 proxy 键（不区分大小写）并返回其原值。
func extractHeaderProxy(headers map[string]string) (string, bool) {
	for key, value := range headers {
		if strings.EqualFold(strings.TrimSpace(key), "proxy") {
			delete(headers, key)
			return value, true
		}
	}
	return "", false
}

func optionString(opts map[string]any, wanted string) string {
	if value, ok := optionValue(opts, wanted); ok {
		return strings.TrimSpace(fmt.Sprint(value))
	}
	return ""
}

func optionValue(opts map[string]any, wanted string) (any, bool) {
	for key, value := range opts {
		if strings.EqualFold(strings.TrimSpace(key), wanted) {
			return value, true
		}
	}
	return nil, false
}

func applyBody(req *fetch.Request, value any) string {
	switch body := value.(type) {
	case string:
		req.Body = []byte(body)
		if headerValue(req.Headers, "Content-Type") != "" {
			// 已有 Content-Type 时保留该头，但仍返回 body 原文，
			// 由 Build 统一按 charset 选项做表单编码（S09-05），charset 不因显式头而失效。
			return body
		}
		if isStructuredBody(body) {
			setHeader(req.Headers, "Content-Type", "application/json; charset=UTF-8")
			return ""
		}
		setHeader(req.Headers, "Content-Type", "application/x-www-form-urlencoded")
		return body
	case map[string]any, []any:
		if encoded, err := json.Marshal(body); err == nil {
			req.Body = encoded
			if headerValue(req.Headers, "Content-Type") == "" {
				setHeader(req.Headers, "Content-Type", "application/json; charset=UTF-8")
			}
		}
	}
	return ""
}

func mergeHeaders(headers map[string]string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, raw := range typed {
			setHeader(headers, key, fmt.Sprint(raw))
		}
	case map[string]string:
		for key, raw := range typed {
			setHeader(headers, key, raw)
		}
	case string:
		nested := map[string]string{}
		if err := json.Unmarshal([]byte(typed), &nested); err == nil {
			for key, raw := range nested {
				setHeader(headers, key, raw)
			}
			return
		}
		for key, raw := range urlrule.ParseAttributeObject(typed) {
			setHeader(headers, key, fmt.Sprint(raw))
		}
	}
}

func setHeader(headers map[string]string, key string, value string) {
	for existing := range headers {
		if strings.EqualFold(existing, key) && existing != key {
			delete(headers, existing)
		}
	}
	headers[key] = value
}

func cloneHeaders(headers map[string]string) map[string]string {
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		out[key] = value
	}
	return out
}

func headerValue(headers map[string]string, wanted string) string {
	for key, value := range headers {
		if strings.EqualFold(key, wanted) {
			return value
		}
	}
	return ""
}

func positiveInt(value any) int {
	var parsed int
	switch typed := value.(type) {
	case float64:
		parsed = int(typed)
	case int:
		parsed = typed
	case int64:
		parsed = int(typed)
	case string:
		parsed, _ = strconv.Atoi(strings.TrimSpace(typed))
	}
	if parsed > 0 {
		return parsed
	}
	return 0
}

func isStructuredBody(body string) bool {
	body = strings.TrimSpace(body)
	return json.Valid([]byte(body)) || strings.HasPrefix(body, "<")
}

func optionTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		text := strings.TrimSpace(strings.ToLower(typed))
		return text != "" && text != "false" && text != "0"
	case float64:
		return typed != 0
	case int:
		return typed != 0
	default:
		return true
	}
}

func zeroLike(value any) bool {
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
