package logging

import (
	"net/http"
	"regexp"
	"strings"
)

const RedactedValue = "[REDACTED]"

var sensitiveHeaders = map[string]struct{}{
	"authorization": {},
	"cookie":        {},
	"set-cookie":    {},
}

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)[^\s]+`),
	regexp.MustCompile(`(?i)(cookie:\s*)[^\n\r]+`),
	regexp.MustCompile(`(?i)(set-cookie:\s*)[^\n\r]+`),
	regexp.MustCompile(`(?i)(token=)[^\s&]+`),
}

func RedactHeaders(headers http.Header) http.Header {
	redacted := make(http.Header, len(headers))
	for key, values := range headers {
		copied := append([]string(nil), values...)
		if _, ok := sensitiveHeaders[strings.ToLower(key)]; ok {
			for i := range copied {
				copied[i] = RedactedValue
			}
		}
		redacted[key] = copied
	}
	return redacted
}

func RedactString(value string) string {
	redacted := value
	for _, pattern := range sensitivePatterns {
		redacted = pattern.ReplaceAllString(redacted, "${1}"+RedactedValue)
	}
	return redacted
}
