package logging

import (
	"regexp"
	"strings"
)

const RedactedValue = "[REDACTED]"

// sensitiveHeaderNames 中的请求/响应头整行值都会被脱敏。
var sensitiveHeaderNames = []string{
	"proxy-authorization",
	"authorization",
	"set-cookie",
	"cookie",
	"x-api-key",
	"x-auth-token",
	"x-access-token",
	"x-csrf-token",
	"x-xsrf-token",
}

type redactPattern struct {
	pattern     *regexp.Regexp
	replacement string
}

var sensitivePatterns = []redactPattern{
	{
		// 形如 "Authorization: Bearer xxx" 的头部行，脱敏到行尾。
		pattern:     regexp.MustCompile(`(?i)\b((?:` + strings.Join(sensitiveHeaderNames, "|") + `)\s*:\s*)[^\r\n]+`),
		replacement: "${1}" + RedactedValue,
	},
	{
		// URL query 或 key=value 文本中的凭证参数。
		pattern:     regexp.MustCompile(`(?i)\b((?:access_|refresh_|auth_|id_)?token|api[_-]?key|apikey|key|password|passwd|pwd|(?:client_)?secret)=[^&\s#"']+`),
		replacement: "${1}=" + RedactedValue,
	},
	{
		// URL userinfo：scheme://user:password@host。
		pattern:     regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.\-]*://[^/\s:@]+:)[^@\s/]+@`),
		replacement: "${1}" + RedactedValue + "@",
	},
}

// RedactString 对日志、调试事件与错误消息中的凭证做脱敏。
func RedactString(value string) string {
	redacted := value
	for _, p := range sensitivePatterns {
		redacted = p.pattern.ReplaceAllString(redacted, p.replacement)
	}
	return redacted
}
