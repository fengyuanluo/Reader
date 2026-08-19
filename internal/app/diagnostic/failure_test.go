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
			err:       errors.New(`Get "https://x": net/http: TLS handshake timeout`),
			fallback:  FailureSearchFailed,
			wantCode:  FailureTimeout,
			wantClass: FailureClassNetworkTimeout,
		},
		{
			name:      "standalone eof",
			err:       errors.New("EOF"),
			fallback:  FailureSearchFailed,
			wantCode:  FailureSearchFailed,
			wantClass: FailureClassNetworkTransient,
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
			wantClass: FailureClassUnsupportedBrowser,
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
	if details.Class != FailureClassUnsupportedBrowser {
		t.Fatalf("class = %q, want %q", details.Class, FailureClassUnsupportedBrowser)
	}
}

func TestClassifyFailureDoesNotTreatGenericStageFailureAsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		fallback string
	}{
		{name: "search", fallback: FailureSearchFailed},
		{name: "discovery", fallback: FailureDiscoveryFailed},
		{name: "toc", fallback: FailureTocFailed},
		{name: "content", fallback: FailureContentFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := ClassifyFailure(errors.New("upstream rule failed"), tt.fallback)
			if details.Class != FailureClassUnknown {
				t.Fatalf("class = %q, want %q", details.Class, FailureClassUnknown)
			}
		})
	}
}

func TestClassifyFailurePreservesAnyUpstreamHTTPStatus(t *testing.T) {
	for _, status := range []string{"405", "410", "429", "444", "451", "502", "503", "504", "520", "521", "522"} {
		t.Run(status, func(t *testing.T) {
			details := ClassifyFailure(errors.New("unexpected status: "+status), FailureSearchFailed)
			if details.Class != "source_upstream_http_"+status {
				t.Fatalf("class = %q", details.Class)
			}
		})
	}
}
