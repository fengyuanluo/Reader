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

func ParseAttributeObject(raw string) map[string]any {
	out := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &out); err == nil {
		return out
	}
	loose := normalizeLooseAttributeJSON(raw)
	if loose != raw {
		_ = json.Unmarshal([]byte(loose), &out)
	}
	return out
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
	parsed := ParseCustom(raw)
	parsed.URL = absolutePlain(baseURL, parsed.URL)
	return parsed.String()
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
