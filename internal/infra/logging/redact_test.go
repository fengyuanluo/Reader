package logging

import (
	"net/http"
	"strings"
	"testing"
)

func TestRedactHeadersMasksSensitiveHeaders(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer secret")
	headers.Set("Cookie", "sid=secret")
	headers.Set("Set-Cookie", "sid=secret")
	headers.Set("X-Trace-Id", "trace-1")

	redacted := RedactHeaders(headers)

	for _, key := range []string{"Authorization", "Cookie", "Set-Cookie"} {
		if got := redacted.Get(key); got != RedactedValue {
			t.Fatalf("%s = %q, want %q", key, got, RedactedValue)
		}
	}
	if got := redacted.Get("X-Trace-Id"); got != "trace-1" {
		t.Fatalf("X-Trace-Id = %q, want trace-1", got)
	}
	if headers.Get("Authorization") != "Bearer secret" {
		t.Fatal("RedactHeaders mutated original header")
	}
}

func TestRedactStringMasksTokenLikeValues(t *testing.T) {
	input := "Authorization: Bearer abc Cookie: sid=secret Set-Cookie: sid=secret token=secret"
	output := RedactString(input)

	if output == input {
		t.Fatal("RedactString did not redact sensitive values")
	}
	for _, secret := range []string{"abc", "sid=secret", "token=secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("redacted output still contains %q: %s", secret, output)
		}
	}
}

func TestRedactStringMasksMultilineCookies(t *testing.T) {
	input := "Cookie: sid=abc\nSet-Cookie: refresh=secret; Path=/"
	output := RedactString(input)

	for _, secret := range []string{"sid=abc", "refresh=secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("redacted output still contains %q: %s", secret, output)
		}
	}
}
