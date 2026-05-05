package urlrule

import (
	"bytes"
	"net/url"
	"strings"

	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

func EncodeURI(value string, charsetName string) string {
	return encodeComponent(value, charsetName)
}

func EncodeQueryString(query string, charsetName string) string {
	return encodeParamPairs(query, charsetName)
}

func EncodeFormString(form string, charsetName string) string {
	return encodeParamPairs(form, charsetName)
}

func EncodeURLQuery(rawURL string, charsetName string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.RawQuery == "" {
		return rawURL
	}
	parsed.RawQuery = EncodeQueryString(parsed.RawQuery, charsetName)
	return parsed.String()
}

func encodeParamPairs(params string, charsetName string) string {
	params = strings.TrimSpace(params)
	if params == "" {
		return ""
	}
	parts := strings.Split(params, "&")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		key = encodeComponentIfNeeded(key, charsetName)
		if !ok {
			out = append(out, key)
			continue
		}
		out = append(out, key+"="+encodeComponentIfNeeded(value, charsetName))
	}
	return strings.Join(out, "&")
}

func encodeComponentIfNeeded(value string, charsetName string) string {
	if looksAlreadyEncoded(value) {
		return value
	}
	return encodeComponent(value, charsetName)
}

func encodeComponent(value string, charsetName string) string {
	if strings.EqualFold(strings.TrimSpace(charsetName), "escape") {
		return escapeLegacy(value)
	}
	return url.QueryEscape(encodeStringWithCharset(value, charsetName))
}

func encodeStringWithCharset(value string, charsetName string) string {
	charsetName = strings.TrimSpace(charsetName)
	if charsetName == "" || strings.EqualFold(charsetName, "utf-8") || strings.EqualFold(charsetName, "utf8") {
		return value
	}
	enc, err := htmlindex.Get(charsetName)
	if err != nil || enc == nil {
		return value
	}
	encoded, _, err := transform.String(enc.NewEncoder(), value)
	if err != nil {
		return value
	}
	return encoded
}

func looksAlreadyEncoded(value string) bool {
	if !strings.Contains(value, "%") && !strings.Contains(value, "+") {
		return false
	}
	decoded, err := url.QueryUnescape(value)
	if err != nil {
		return false
	}
	normalizedInput := normalizeEscaped(value)
	normalizedRoundTrip := normalizeEscaped(url.QueryEscape(decoded))
	return normalizedInput == normalizedRoundTrip
}

func normalizeEscaped(value string) string {
	return strings.ReplaceAll(value, "+", "%20")
}

func escapeLegacy(src string) string {
	var buf bytes.Buffer
	for _, r := range src {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
			buf.WriteRune(r)
		case r < 16:
			buf.WriteString("%0")
			buf.WriteString(strings.ToLower(strconvItoaBase(int(r), 16)))
		case r < 256:
			buf.WriteString("%")
			buf.WriteString(strings.ToLower(strconvItoaBase(int(r), 16)))
		default:
			buf.WriteString("%u")
			buf.WriteString(strings.ToLower(strconvItoaBase(int(r), 16)))
		}
	}
	return buf.String()
}

func strconvItoaBase(value int, base int) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if value == 0 {
		return "0"
	}
	out := make([]byte, 0, 8)
	for value > 0 {
		out = append(out, digits[value%base])
		value /= base
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}
