package security_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	replaceapp "reader/internal/app/replace"
	"reader/internal/domain/fetch"
	"reader/internal/domain/replace"
	"reader/internal/infra/fetcher"
	"reader/internal/infra/logging"
	"reader/internal/infra/script"
)

func TestFetcherBlocksPrivateAddressSSRF(t *testing.T) {
	resolver := func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	f := fetcher.NewHTTPFetcher(fetcher.WithResolver(resolver))

	_, err := f.Fetch(context.Background(), fetch.Request{URL: "http://private.example.test/secret"})
	if !errors.Is(err, fetch.ErrBlocked) {
		t.Fatalf("Fetch() error = %v, want ErrBlocked", err)
	}
}

func TestFetcherBlocksRedirectSSRF(t *testing.T) {
	resolver := func(host string) ([]net.IP, error) {
		switch host {
		case "public.example.test":
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		case "private.example.test":
			return []net.IP{net.ParseIP("10.0.0.1")}, nil
		default:
			return nil, errors.New("unexpected host")
		}
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://private.example.test/secret"}},
			Body:       http.NoBody,
			Request:    req,
		}, nil
	})}
	f := fetcher.NewHTTPFetcher(fetcher.WithHTTPClient(client), fetcher.WithResolver(resolver), fetcher.WithMaxRedirects(5))

	_, err := f.Fetch(context.Background(), fetch.Request{URL: "http://public.example.test/start"})
	if !errors.Is(err, fetch.ErrBlocked) {
		t.Fatalf("Fetch() error = %v, want ErrBlocked", err)
	}
}

func TestScriptInfiniteLoopTimesOut(t *testing.T) {
	runtime := script.NewRuntime(script.Policy{Timeout: 10 * time.Millisecond})

	_, err := runtime.Eval(context.Background(), `while (true) {}`, nil)
	if !errors.Is(err, script.ErrScriptTimeout) {
		t.Fatalf("Eval() error = %v, want ErrScriptTimeout", err)
	}
}

func TestScriptCannotAccessHostEscapePrimitives(t *testing.T) {
	runtime := script.NewRuntime(script.Policy{})

	got, err := runtime.Eval(context.Background(), `typeof require + "," + typeof process + "," + typeof fetch + "," + typeof Deno`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	if got != "undefined,undefined,undefined,undefined" {
		t.Fatalf("got = %#v", got)
	}
}

func TestRegexReplacementRejectsOversizedInput(t *testing.T) {
	text := strings.Repeat("a", 2*1024*1024+1)

	_, err := (&replaceapp.Manager{}).Test(context.Background(), replace.ReplaceRule{Pattern: "a", Replacement: "b", IsRegex: true}, text)
	if !errors.Is(err, replaceapp.ErrRegexLimitExceeded) {
		t.Fatalf("Test() error = %v, want ErrRegexLimitExceeded", err)
	}
}

func TestLogRedactionMasksSensitiveValues(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer secret")
	headers.Set("Cookie", "sid=secret")
	headers.Set("Set-Cookie", "refresh=secret")

	redacted := logging.RedactHeaders(headers)
	for _, key := range []string{"Authorization", "Cookie", "Set-Cookie"} {
		if got := redacted.Get(key); got != logging.RedactedValue {
			t.Fatalf("%s = %q", key, got)
		}
	}
	message := logging.RedactString("Authorization: Bearer abc\nCookie: sid=secret\nSet-Cookie: refresh=secret; Path=/ token=secret")
	for _, secret := range []string{"abc", "sid=secret", "refresh=secret", "token=secret"} {
		if strings.Contains(message, secret) {
			t.Fatalf("redacted message still contains %q: %s", secret, message)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
