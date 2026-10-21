package logging

import (
	"regexp"
	"strings"
)

const RedactedValue = "[REDACTED]"

// sensitiveHeaderNames 中的请求/响应头整行值都会被脱敏。token/password/key 等通用词
// 也在其中（\b 边界保证 x-session-token: 这类头同样命中）。
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
	"token",
	"password",
	"passwd",
	"pwd",
	"secret",
	"api-key",
	"apikey",
}

type redactPattern struct {
	pattern     *regexp.Regexp
	replacement string
}

var sensitivePatterns = []redactPattern{
	{
		// 形如 "Authorization: Bearer xxx"、"token: abc"（冒号分隔的头部/键值行，含引号包裹值），
		// 整行值脱敏到行尾。token/password/key 等通用词已在 sensitiveHeaderNames 中。
		pattern:     regexp.MustCompile(`(?i)\b((?:` + strings.Join(sensitiveHeaderNames, "|") + `)\s*:\s*)[^\r\n]+`),
		replacement: "${1}" + RedactedValue,
	},
	{
		// key=value 与 key : value（等号或冒号分隔，分隔符保留）；
		// 值允许双引号/单引号包裹形态（token="abc"）。头部行（如 Cookie:）由上一条模式整行脱敏。
		pattern:     regexp.MustCompile(`(?i)\b((?:access_|refresh_|auth_|id_)?token|api[_-]?key|apikey|key|password|passwd|pwd|(?:client_)?secret)(\s*[:=]\s*)("([^&\s\r\n#"']*)"|'([^&\s\r\n#"']*)'|[^&\s\r\n#"']+)`),
		replacement: "${1}${2}" + RedactedValue,
	},
	{
		// 自由文本中的认证 scheme 密钥：Bearer sk-live-...、Basic dXNlcjpwYXNz、Bearer eyJ...（JWT）。
		pattern:     regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9\-._~+/]+=*`),
		replacement: "${1} " + RedactedValue,
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
