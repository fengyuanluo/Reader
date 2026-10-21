package urlrule

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

type CustomURL struct {
	URL       string
	Attribute map[string]any
}

func ParseCustom(raw string) CustomURL {
	raw = strings.TrimSpace(raw)
	base, attrRaw, ok := splitAttribute(raw)
	out := CustomURL{URL: base, Attribute: map[string]any{}}
	if !ok {
		return out
	}
	if objectPrefix, ok := firstJSONObject(attrRaw); ok {
		attrRaw = objectPrefix
	}
	out.Attribute = ParseAttributeObject(attrRaw)
	return out
}

// ParseHeaderObject 解析独立的 header JSON（书源 Header 字段整串，非 URL 选项对象）：
// 键保持原大小写（HTTP 头名大小写敏感），按 raw 出现顺序遍历、后键覆盖前键（GSON 语义），
// 值按 GSON Double 语义解码。仅大小写不同的重复键取后键，取值确定。
// 非对象或解析失败返回空 map。
func ParseHeaderObject(raw string) map[string]any {
	out := map[string]any{}
	if headers, ok := parseOrderedObject(raw); ok {
		return headers
	}
	loose := normalizeLooseAttributeJSON(raw)
	if loose != raw {
		if headers, ok := parseOrderedObject(loose); ok {
			return headers
		}
	}
	return out
}

// parseOrderedObject 与 normalizeAttributeObject 同构，但不归一顶层键大小写。
func parseOrderedObject(raw string) (map[string]any, bool) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, false
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, false
	}
	out := map[string]any{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, false
		}
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		out[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, false
	}
	return out, true
}

func ParseAttributeObject(raw string) map[string]any {
	// 对齐 Legado GSON 语义：按 raw 出现顺序遍历对象键，后键覆盖前键；
	// 键统一归一为小写，消费方直接小写查表，避免仅大小写不同的重复键取值不确定。
	out := map[string]any{}
	if normalized, ok := normalizeAttributeObject(raw); ok {
		return normalized
	}
	loose := normalizeLooseAttributeJSON(raw)
	if loose != raw {
		if normalized, ok := normalizeAttributeObject(loose); ok {
			return normalized
		}
	}
	return out
}

// normalizeAttributeObject 用 json.Decoder 按 raw 顺序遍历对象层的键值：
// 顶层选项键 strings.ToLower 后写入 map，后键覆盖前键（GSON 语义），值按 GSON Double 语义
// 解码为 float64。S07-05 的键归一仅作用于 optionValue 查找键（method/type/charset/retry/webview 等）；
// headers 等值内的 HTTP 头名不在本层归一，保持原大小写（HTTP 头名大小写敏感语义由消费方保留）。
// 非对象或解析失败返回 ok=false。
func normalizeAttributeObject(raw string) (map[string]any, bool) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, false
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, false
	}
	out := map[string]any{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, false
		}
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		out[strings.ToLower(key)] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, false
	}
	return out, true
}

var looseObjectKeyPattern = regexp.MustCompile(`([,{]\s*)([A-Za-z_][A-Za-z0-9_-]*)(\s*:)`)

func normalizeLooseAttributeJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.NewReplacer("“", `"`, "”", `"`, "‘", `'`, "’", `'`).Replace(raw)
	raw = singleQuotedStringsToDouble(raw)
	raw = looseObjectKeyPattern.ReplaceAllString(raw, `${1}"${2}"${3}`)
	return raw
}

func singleQuotedStringsToDouble(raw string) string {
	var b strings.Builder
	inSingle := false
	escaped := false
	for _, r := range raw {
		switch {
		case escaped:
			if inSingle && r == '"' {
				b.WriteRune('\\')
			}
			b.WriteRune(r)
			escaped = false
		case r == '\\':
			b.WriteRune(r)
			escaped = true
		case r == '\'':
			if inSingle {
				b.WriteRune('"')
				inSingle = false
			} else {
				b.WriteRune('"')
				inSingle = true
			}
		default:
			if inSingle && r == '"' {
				b.WriteRune('\\')
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (u CustomURL) String() string {
	if len(u.Attribute) == 0 {
		return u.URL
	}
	raw, err := json.Marshal(u.Attribute)
	if err != nil {
		return u.URL
	}
	return u.URL + "," + string(raw)
}

func Absolute(baseURL string, raw string) string {
	// S06-15：无属性段（无逗号）的普通 URL 走快速路径，跳过 ParseCustom
	// 的 JSON 解析与宽松正则修复；GetURLList/GetURL 对每个候选都调本函数。
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.IndexByte(raw, ',') < 0 {
		return absolutePlain(baseURL, raw)
	}
	parsed := ParseCustom(raw)
	parsed.URL = absolutePlain(baseURL, parsed.URL)
	return parsed.String()
}

func AbsolutePreservingOptions(baseURL string, raw string) string {
	raw = strings.TrimSpace(raw)
	for i := 0; i < len(raw); i++ {
		if raw[i] != ',' || !strings.HasPrefix(strings.TrimSpace(raw[i+1:]), "{") {
			continue
		}
		absolute := absolutePlain(baseURL, raw[:i])
		if absolute == "" {
			return ""
		}
		return absolute + raw[i:]
	}
	return absolutePlain(baseURL, raw)
}

func absolutePlain(baseURL string, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "data:") {
		return raw
	}
	if strings.HasPrefix(lower, "javascript") {
		return ""
	}
	baseURL = strings.SplitN(baseURL, ",", 2)[0]
	if baseURL == "" {
		return raw
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return raw
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return base.ResolveReference(ref).String()
}

func splitAttribute(raw string) (string, string, bool) {
	for i := 0; i < len(raw); i++ {
		if raw[i] != ',' {
			continue
		}
		rest := strings.TrimSpace(raw[i+1:])
		if strings.HasPrefix(rest, "{") {
			return strings.TrimSpace(raw[:i]), rest, true
		}
	}
	return raw, "", false
}

func firstJSONObject(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "{") {
		return "", false
	}
	depth := 0
	inSingle := false
	inDouble := false
	escaped := false
	for i, r := range raw {
		switch {
		case escaped:
			escaped = false
			continue
		case r == '\\':
			escaped = true
			continue
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			continue
		case r == '"' && !inSingle:
			inDouble = !inDouble
			continue
		case inSingle || inDouble:
			continue
		case r == '{':
			depth++
		case r == '}':
			depth--
			if depth == 0 {
				return raw[:i+1], true
			}
		}
	}
	return "", false
}
