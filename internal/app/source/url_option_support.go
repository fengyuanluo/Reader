package sourceapp

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"reader/internal/domain/fetch"
)

var ErrUnsupportedURLOption = errors.New("unsupported url option")

// supportedURLOptions 为书源 URL 选项（Legado AnalyzeUrl.UrlOption）中 Reader 支持的键，按小写比较。
// serverID 只用于 Legado 的 WebDav 备份，webJs / webViewDelayTime 只在 webView 生效时有意义，
// 三者在此接受但不产生效果；webView 本身不支持，非空值仍会报错。
var supportedURLOptions = map[string]struct{}{
	"method":           {},
	"body":             {},
	"headers":          {},
	"type":             {},
	"charset":          {},
	"origin":           {},
	"retry":            {},
	"proxy":            {},
	"dnsip":            {},
	"serverid":         {},
	"webjs":            {},
	"webviewdelaytime": {},
}

func ValidateSupportedURLOptions(opts map[string]any) error {
	for key, value := range opts {
		if _, ok := supportedURLOptions[strings.ToLower(strings.TrimSpace(key))]; ok {
			continue
		}
		if isZeroLikeOptionValue(value) {
			continue
		}
		return fmt.Errorf("%w: %s", ErrUnsupportedURLOption, key)
	}
	return nil
}

// ApplyNetworkOptions 把代理与 DNS 固定配置写入请求：
// 书源请求头中的 proxy 键按 Legado AnalyzeUrl 约定移出请求头作为代理；URL 选项 proxy / dnsIp 优先。
func ApplyNetworkOptions(req *fetch.Request, opts map[string]any) {
	if proxy, ok := req.Headers["proxy"]; ok {
		delete(req.Headers, "proxy")
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

// LooksLikeFormBody 判断请求体是否按 Legado AnalyzeUrl 的约定当作表单编码发送：
// JSON / XML 结构（以 { [ < 开头）不视为表单，其余含 = 或 & 的文本按表单编码。
// 搜索、书籍信息与正文三条抓取链路共用同一份实现，避免行为漂移。
func LooksLikeFormBody(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}
	if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") || strings.HasPrefix(body, "<") {
		return false
	}
	return strings.Contains(body, "=") || strings.Contains(body, "&")
}

// ApplyRetryOption 解析 URL 选项 retry，按 Legado AnalyzeUrl 的宽化语义接受数字与数字字符串。
func ApplyRetryOption(req *fetch.Request, value any) {
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

func isZeroLikeOptionValue(value any) bool {
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
