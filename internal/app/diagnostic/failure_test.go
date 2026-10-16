package diagnosticapp

import (
	"context"
	"errors"
	"testing"

	bookapp "reader/internal/app/book"
	contentapp "reader/internal/app/content"
	"reader/internal/domain/fetch"
)

func TestClassifyFailureCoversCommonFailureFamilies(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		fallback  string
		wantCode  string
		wantClass string
	}{
		{
			name:      "script syntax",
			err:       errors.New("script_failed: e2e: SyntaxError: Unexpected token"),
			wantCode:  FailureJSFailed,
			wantClass: FailureClassSourceRuleScriptSyntax,
		},
		{
			name:      "dns failure",
			err:       errors.New(`Get "https://x": dial tcp: lookup source.test: no such host`),
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassNetworkDNSFailure,
		},
		{
			name:      "tls failure",
			err:       errors.New(`Get "https://x": tls: failed to verify certificate: x509: certificate has expired or is not yet valid`),
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassNetworkTLSInvalid,
		},
		{
			name:      "tls handshake timeout",
			err:       errors.New(`Get "https://ocean.shuqireader.com/api/bcspub/qswebapi/book/chapterlist?bookId=6167443": net/http: TLS handshake timeout`),
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassNetworkTLSInvalid,
		},
		{
			name:      "search warning tls handshake timeout",
			err:       errors.New(`empty search result; warnings: search_failed: Get "https://api-x.shrtxs.cn/qd/search.php?keyword=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9&page=1": net/http: TLS handshake timeout`),
			fallback:  FailureSearchFailed,
			wantCode:  FailureSearchFailed,
			wantClass: FailureClassNetworkTLSInvalid,
		},
		{
			name:      "content eof",
			err:       errors.New(`Post "http://119.45.176.116:5006/chapterContent": EOF`),
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassNetworkConnectionReset,
		},
		{
			name:      "search warning eof",
			err:       errors.New(`empty search result; warnings: search_failed: Get "https://book.mywebos.cn/bk/search?name=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9": EOF`),
			fallback:  FailureSearchFailed,
			wantCode:  FailureSearchFailed,
			wantClass: FailureClassNetworkConnectionReset,
		},
		{
			name:      "unexpected eof",
			err:       errors.New(`Post "https://x/chapterContent": unexpected EOF`),
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassNetworkConnectionReset,
		},
		{
			name:      "connection refused",
			err:       errors.New(`Get "http://www.shencou.com/modules/article/search.php?searchtype=articlename": dial tcp 0.0.0.0:80: connect: connection refused`),
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassNetworkConnectionReset,
		},
		{
			name:      "upstream 404",
			err:       errors.New("unexpected status: 404"),
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassSourceUpstreamHTTP404,
		},
		{
			name:      "unsupported browser flow option",
			err:       errors.New("unsupported url option: webView"),
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassSourceUnsupportedRule,
		},
		{
			name:      "search empty",
			err:       errors.New("empty search result"),
			fallback:  FailureSearchFailed,
			wantCode:  FailureSearchFailed,
			wantClass: FailureClassSourceSearchEmptyResult,
		},
		{
			name:      "toc empty",
			err:       bookapp.ErrTocEmpty,
			fallback:  FailureWebsiteFailed,
			wantCode:  FailureTocFailed,
			wantClass: FailureClassSourceTocEmpty,
		},
		{
			name:      "content empty",
			err:       contentapp.ErrContentEmpty,
			fallback:  FailureWebsiteFailed,
			wantCode:  FailureContentFailed,
			wantClass: FailureClassSourceContentEmpty,
		},
		{
			name:      "blocked",
			err:       fetch.ErrBlocked,
			fallback:  FailureWebsiteFailed,
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassSecurityPolicyBlocked,
		},
		{
			name:      "deadline",
			err:       context.DeadlineExceeded,
			wantCode:  FailureTimeout,
			wantClass: FailureClassNetworkTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := ClassifyFailure(tt.err, tt.fallback)
			if details.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", details.Code, tt.wantCode)
			}
			if details.Class != tt.wantClass {
				t.Fatalf("class = %q, want %q", details.Class, tt.wantClass)
			}
			if tt.err != nil && details.Message == "" {
				t.Fatal("message is empty")
			}
		})
	}
}

func TestClassifyFailureAllowsStageFallbackWhenMessageIsGenericWebsiteFailure(t *testing.T) {
	details := ClassifyFailure(errors.New("unsupported url option: webView"), FailureSearchFailed)
	if details.Code != FailureSearchFailed {
		t.Fatalf("code = %q, want %q", details.Code, FailureSearchFailed)
	}
	if details.Class != FailureClassSourceUnsupportedRule {
		t.Fatalf("class = %q, want %q", details.Class, FailureClassSourceUnsupportedRule)
	}
}

func TestClassifyFailureKeepsExistingCorrectClasses(t *testing.T) {
	tests := []struct {
		name      string
		message   string
		fallback  string
		wantCode  string
		wantClass string
	}{
		{
			name:      "x509 tls",
			message:   `Get "https://x": tls: failed to verify certificate: x509: certificate signed by unknown authority`,
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassNetworkTLSInvalid,
		},
		{
			name:      "x509 tls with search warning",
			message:   `empty search result; warnings: search_failed: Get "https://www.deqibook.com/tag/?key=%E6%96%97%E7%A0%B4%E8%8B%8D%E7%A9%B9": tls: failed to verify certificate: x509: certificate signed by unknown authority`,
			fallback:  FailureSearchFailed,
			wantCode:  FailureSearchFailed,
			wantClass: FailureClassNetworkTLSInvalid,
		},
		{
			name:      "connection reset",
			message:   `Post "https://x": read tcp 1.1.1.1:1->2.2.2.2:2: read: connection reset by peer`,
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassNetworkConnectionReset,
		},
		{
			name:      "dns no such host",
			message:   `Get "https://x": dial tcp: lookup source.test: no such host`,
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassNetworkDNSFailure,
		},
		{
			name:      "dns server misbehaving",
			message:   `empty search result; warnings: search_failed: Post "https://wodushu.com/search/": dial tcp: lookup wodushu.com on 223.5.5.5:53: server misbehaving`,
			fallback:  FailureSearchFailed,
			wantCode:  FailureSearchFailed,
			wantClass: FailureClassNetworkDNSFailure,
		},
		{
			name:      "network timeout",
			message:   `Get "https://www.linovel.net/book/103712/37507.html": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`,
			wantCode:  FailureTimeout,
			wantClass: FailureClassNetworkTimeout,
		},
		{
			name:      "upstream 400",
			message:   "unexpected status: 400",
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassSourceUpstreamHTTP400,
		},
		{
			name:      "upstream 403 with search warning",
			message:   `empty search result; warnings: search_failed: unexpected status: 403`,
			fallback:  FailureSearchFailed,
			wantCode:  FailureSearchFailed,
			wantClass: FailureClassSourceUpstreamHTTP403,
		},
		{
			name:      "upstream 500",
			message:   "unexpected status: 500",
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassSourceUpstreamHTTP500,
		},
		{
			name:      "upstream empty json",
			message:   "unexpected end of JSON input",
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassSourceUpstreamEmptyJSON,
		},
		{
			name:      "script eof stays upstream empty json",
			message:   `script_failed: e2e: SyntaxError: Unexpected end of JSON input (EOF) at parse (native)`,
			wantCode:  FailureJSFailed,
			wantClass: FailureClassSourceUpstreamEmptyJSON,
		},
		{
			name:      "upstream non json",
			message:   `script_failed: e2e: SyntaxError: invalid character 'G' looking for beginning of value at parse (native)`,
			wantCode:  FailureJSFailed,
			wantClass: FailureClassSourceUpstreamNonJSON,
		},
		{
			name:      "script syntax error",
			message:   "script_failed: e2e: SyntaxError: Unexpected token",
			wantCode:  FailureJSFailed,
			wantClass: FailureClassSourceRuleScriptSyntax,
		},
		{
			name:      "script failed with search warning",
			message:   `empty search result; warnings: search_failed: script_failed: e2e: TypeError: Cannot read property 'join' of undefined or null at <eval>:4:12(22)`,
			fallback:  FailureSearchFailed,
			wantCode:  FailureJSFailed,
			wantClass: FailureClassSourceRuleScriptFailed,
		},
		{
			name:      "authorization 401",
			message:   "unexpected status: 401",
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassSourceAuthorizationFailed,
		},
		{
			name:      "authorization 4005",
			message:   `{"msg":"认证失败","code":4005,"data":[]}`,
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassSourceAuthorizationFailed,
		},
		{
			name:      "security policy blocked",
			message:   "fetch blocked",
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassSecurityPolicyBlocked,
		},
		{
			name:      "unsupported rule feature",
			message:   "unsupported url option: webView",
			wantCode:  FailureWebsiteFailed,
			wantClass: FailureClassSourceUnsupportedRule,
		},
		{
			name:      "search empty result plain",
			message:   "empty search result",
			fallback:  FailureSearchFailed,
			wantCode:  FailureSearchFailed,
			wantClass: FailureClassSourceSearchEmptyResult,
		},
		{
			name:      "search empty result with unrecognized warning",
			message:   `empty search result; warnings: search_failed: custom rule returned no rows`,
			fallback:  FailureSearchFailed,
			wantCode:  FailureSearchFailed,
			wantClass: FailureClassSourceSearchEmptyResult,
		},
		{
			name:      "discovery empty result",
			message:   "empty discovery result",
			fallback:  FailureDiscoveryFailed,
			wantCode:  FailureDiscoveryFailed,
			wantClass: FailureClassSourceDiscoveryEmptyResult,
		},
		{
			name:      "toc empty",
			message:   "toc_empty",
			fallback:  FailureTocFailed,
			wantCode:  FailureTocFailed,
			wantClass: FailureClassSourceTocEmpty,
		},
		{
			name:      "content empty",
			message:   "empty content",
			fallback:  FailureContentFailed,
			wantCode:  FailureContentFailed,
			wantClass: FailureClassSourceContentEmpty,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := ClassifyFailure(errors.New(tt.message), tt.fallback)
			if details.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", details.Code, tt.wantCode)
			}
			if details.Class != tt.wantClass {
				t.Fatalf("class = %q, want %q", details.Class, tt.wantClass)
			}
		})
	}
}
