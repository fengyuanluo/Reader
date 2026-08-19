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
	ErrUnsupportedURLOption   = errors.New("unsupported url option")
	ErrScriptUnavailable      = errors.New("script runtime is not configured")
	ErrUnsupportedSourceProxy = errors.New("source proxy is not supported")
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
	if value, ok := optionValue(custom.Attribute, "type"); ok && strings.TrimSpace(fmt.Sprint(value)) != "" {
		base.EncodeBodyAsHex = true
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
	return base, nil
}

func Fetch(ctx context.Context, fetcher fetch.Fetcher, req fetch.Request, eval Evaluator) (*fetch.Response, error) {
	resp, err := fetcher.Fetch(ctx, req)
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

func Validate(opts map[string]any) error {
	unsupported := map[string]struct{}{
		"webjs": {}, "dnsip": {}, "proxy": {}, "webviewdelaytime": {}, "serverid": {},
	}
	for key, value := range opts {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, blocked := unsupported[normalized]; blocked && !zeroLike(value) {
			return fmt.Errorf("%w: %s", ErrUnsupportedURLOption, key)
		}
	}
	return nil
}

func SanitizeSourceHeaders(headers map[string]string) error {
	for key, value := range headers {
		if !strings.EqualFold(strings.TrimSpace(key), "proxy") {
			continue
		}
		delete(headers, key)
		if strings.TrimSpace(value) != "" {
			return ErrUnsupportedSourceProxy
		}
	}
	return nil
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
			return ""
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
