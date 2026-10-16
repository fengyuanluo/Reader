package diagnosticapp

import (
	"context"
	"errors"
	"regexp"
	"strings"

	bookapp "reader/internal/app/book"
	contentapp "reader/internal/app/content"
	"reader/internal/domain/fetch"
)

var upstreamHTTPStatusPattern = regexp.MustCompile(`(?i)unexpected status:\s*([1-5][0-9]{2})`)

const (
	FailureTimeout         = "timeout"
	FailureJSFailed        = "js_failed"
	FailureWebsiteFailed   = "website_failed"
	FailureSearchFailed    = "search_failed"
	FailureDiscoveryFailed = "discovery_failed"
	FailureInfoFailed      = "info_failed"
	FailureTocFailed       = "toc_failed"
	FailureContentFailed   = "content_failed"
	// FailureSearchRuleEmpty / FailureDiscoveryRuleEmpty 对齐 Legado CheckSourceService 的
	// “搜索链接规则为空”“发现规则为空”分组：书源根本没有配置对应规则，而不是规则执行失败。
	FailureSearchRuleEmpty    = "search_rule_empty"
	FailureDiscoveryRuleEmpty = "discovery_rule_empty"
)

const (
	FailureClassUnknown                    = "implementation_or_rule_failed"
	FailureClassSecurityPolicyBlocked      = "security_policy_blocked"
	FailureClassSourceAuthorizationFailed  = "source_authorization_failed"
	FailureClassNetworkDNSFailure          = "network_dns_failure"
	FailureClassNetworkTLSInvalid          = "network_tls_invalid"
	FailureClassNetworkTimeout             = "network_timeout"
	FailureClassNetworkConnectionReset     = "network_connection_reset"
	FailureClassNetworkTransient           = "network_transient"
	FailureClassSourceUpstreamHTTP400      = "source_upstream_http_400"
	FailureClassSourceUpstreamHTTP401      = "source_upstream_http_401"
	FailureClassSourceUpstreamHTTP403      = "source_upstream_http_403"
	FailureClassSourceUpstreamHTTP404      = "source_upstream_http_404"
	FailureClassSourceUpstreamHTTP500      = "source_upstream_http_500"
	FailureClassSourceUpstreamEmptyJSON    = "source_upstream_empty_json_response"
	FailureClassSourceUpstreamNonJSON      = "source_upstream_non_json_response"
	FailureClassSourceRuleScriptSyntax     = "source_rule_script_syntax_error"
	FailureClassSourceRuleScriptFailed     = "source_rule_script_failed"
	FailureClassSourceUnsupportedRule      = "source_unsupported_rule_feature"
	FailureClassSourceSearchEmptyResult    = "source_search_empty_result"
	FailureClassSourceDiscoveryEmptyResult = "source_discovery_empty_result"
	FailureClassSourceTocEmpty             = "source_toc_empty"
	FailureClassSourceContentEmpty         = "source_content_empty"
	FailureClassSourceDownloadURLsEmpty    = "source_download_urls_empty"
	FailureClassUnsupportedRuntimeAPI      = "unsupported_runtime_api"
	FailureClassUnsupportedBrowser         = "unsupported_browser_capability"
	FailureClassReaderInternal             = "reader_internal"
)

type FailureDetails struct {
	Code    string
	Class   string
	Message string
}

func ClassifyFailure(err error, fallbackCode string) FailureDetails {
	details := FailureDetails{
		Code:  "",
		Class: FailureClassUnknown,
	}
	if err == nil {
		details.Code = fallbackOrWebsiteFailed(fallbackCode)
		return details
	}

	message := strings.TrimSpace(err.Error())
	details.Message = message

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		details.Code = FailureTimeout
		details.Class = FailureClassNetworkTimeout
		return details
	case errors.Is(err, fetch.ErrBlocked):
		details.Class = FailureClassSecurityPolicyBlocked
		if details.Code == "" {
			details.Code = FailureWebsiteFailed
		}
		return details
	case errors.Is(err, bookapp.ErrTocEmpty):
		details.Code = FailureTocFailed
		details.Class = FailureClassSourceTocEmpty
		return details
	case errors.Is(err, contentapp.ErrContentEmpty):
		details.Code = FailureContentFailed
		details.Class = FailureClassSourceContentEmpty
		return details
	}

	lower := strings.ToLower(message)
	classifiedCode := ClassifyFailureCodeFromMessage(lower)
	details.Code = chooseFailureCode(classifiedCode, fallbackCode)
	details.Class = ClassifyFailureClassFromMessage(message, lower, details.Code)
	if details.Code == "" {
		details.Code = FailureWebsiteFailed
	}
	return details
}

func chooseFailureCode(classifiedCode string, fallbackCode string) string {
	switch {
	case classifiedCode != "" && classifiedCode != FailureWebsiteFailed:
		return classifiedCode
	case fallbackCode != "":
		return fallbackCode
	case classifiedCode != "":
		return classifiedCode
	default:
		return FailureWebsiteFailed
	}
}

func fallbackOrWebsiteFailed(fallbackCode string) string {
	if fallbackCode != "" {
		return fallbackCode
	}
	return FailureWebsiteFailed
}

func ClassifyFailureCodeFromMessage(lower string) string {
	switch {
	case strings.Contains(lower, "script_failed"),
		strings.Contains(lower, "script_timeout"),
		strings.Contains(lower, "syntaxerror"),
		strings.Contains(lower, "referenceerror"),
		strings.Contains(lower, "typeerror"):
		return FailureJSFailed
	case strings.HasPrefix(lower, "empty search result"):
		return FailureSearchFailed
	case strings.HasPrefix(lower, "empty discovery result"):
		return FailureDiscoveryFailed
	case strings.Contains(lower, "empty toc"), lower == "toc_empty":
		return FailureTocFailed
	case strings.Contains(lower, "empty content"), lower == "content_empty":
		return FailureContentFailed
	case strings.Contains(lower, "context deadline exceeded"),
		strings.Contains(lower, "i/o timeout"),
		strings.Contains(lower, "client.timeout exceeded while awaiting headers"):
		return FailureTimeout
	default:
		return FailureWebsiteFailed
	}
}

func ClassifyFailureClassFromMessage(message string, lower string, code string) string {
	switch {
	case strings.Contains(message, "认证失败") || strings.Contains(message, `"code":4005`) || strings.Contains(lower, "unexpected status: 401"):
		return FailureClassSourceAuthorizationFailed
	case strings.Contains(lower, "fetch blocked"):
		return FailureClassSecurityPolicyBlocked
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "server misbehaving"):
		return FailureClassNetworkDNSFailure
	case strings.Contains(lower, "tls: failed to verify certificate") ||
		strings.Contains(lower, "x509:") ||
		strings.Contains(lower, "tls handshake timeout"):
		return FailureClassNetworkTLSInvalid
	case strings.Contains(lower, "context deadline exceeded") || strings.Contains(lower, "i/o timeout") ||
		strings.Contains(lower, "client.timeout exceeded while awaiting headers"):
		return FailureClassNetworkTimeout
	// network_connection_reset covers connection-level transport failures:
	// RST, refused ports, and connections closed before a complete response.
	case strings.Contains(lower, "connection reset by peer") ||
		strings.Contains(lower, "connection refused") ||
		isConnectionEOFMessage(lower):
		return FailureClassNetworkConnectionReset
	// network_transient 兜住其余瞬态传输失败（远端新增分类）：裸 EOF 由上面的 connection_reset 口径覆盖。
	case strings.Contains(lower, "connection closed"):
		return FailureClassNetworkTransient
	case upstreamHTTPFailureClass(lower) != "":
		return upstreamHTTPFailureClass(lower)
	case strings.Contains(lower, "unexpected end of json input"):
		return FailureClassSourceUpstreamEmptyJSON
	case strings.Contains(lower, "invalid character") && strings.Contains(lower, "looking for beginning of value at parse"):
		return FailureClassSourceUpstreamNonJSON
	case strings.Contains(lower, "startbrowser") || strings.Contains(lower, "webview") || strings.Contains(lower, "unsupported browser flow"):
		return FailureClassUnsupportedBrowser
	case strings.Contains(lower, "org is not defined") || strings.Contains(lower, "unsupported runtime api") ||
		strings.Contains(lower, "object has no member") || strings.Contains(lower, "referenceerror: org"):
		return FailureClassUnsupportedRuntimeAPI
	case strings.Contains(lower, "empty download urls"):
		return FailureClassSourceDownloadURLsEmpty
	// 规则脚本失败优先于“空结果”文案：带 warnings 的空结果若根因是脚本，归类为脚本失败。
	case strings.Contains(lower, "script_failed:") && strings.Contains(lower, "syntaxerror"):
		return FailureClassSourceRuleScriptSyntax
	case strings.Contains(lower, "script_failed:"):
		return FailureClassSourceRuleScriptFailed
	case strings.HasPrefix(lower, "empty search result"):
		return FailureClassSourceSearchEmptyResult
	case strings.HasPrefix(lower, "empty discovery result"):
		return FailureClassSourceDiscoveryEmptyResult
	case lower == "empty toc" || lower == "toc_empty":
		return FailureClassSourceTocEmpty
	case lower == "empty content" || lower == "content_empty":
		return FailureClassSourceContentEmpty
	case strings.Contains(lower, "unsupported url option") ||
		strings.Contains(lower, "unsupported_rule_feature"):
		return FailureClassSourceUnsupportedRule
	case code == FailureJSFailed:
		return FailureClassSourceRuleScriptFailed
	default:
		return FailureClassUnknown
	}
}

// isConnectionEOFMessage reports Go http transport errors that end with a bare
// EOF (`Post "url": EOF`) or a truncated body ("unexpected EOF"); both mean the
// peer closed the connection before a complete response arrived. It is
// deliberately narrow so script errors like `Unexpected end of JSON input
// (EOF) at parse` keep their upstream classification.
func isConnectionEOFMessage(lower string) bool {
	return lower == "eof" ||
		strings.HasSuffix(lower, ": eof") ||
		strings.Contains(lower, "unexpected eof")
}

func upstreamHTTPFailureClass(message string) string {
	match := upstreamHTTPStatusPattern.FindStringSubmatch(message)
	if len(match) != 2 {
		return ""
	}
	return "source_upstream_http_" + match[1]
}
