package sourceapp

import (
	"errors"
	"fmt"
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
