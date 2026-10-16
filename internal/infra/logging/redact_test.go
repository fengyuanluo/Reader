package logging

import (
	"strings"
	"testing"
)

func TestRedactStringMasksSecrets(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		secrets []string
		keep    []string
	}{
		{name: "bearer authorization", input: "Authorization: Bearer abc", secrets: []string{"abc"}, keep: []string{"Authorization:"}},
		{name: "basic authorization", input: "authorization: Basic dXNlcjpwYXNz", secrets: []string{"dXNlcjpwYXNz"}},
		{name: "proxy authorization", input: "Proxy-Authorization: Basic cHJveHk6cGFzcw==", secrets: []string{"cHJveHk6cGFzcw=="}},
		{name: "x-api-key", input: "X-Api-Key: k-123", secrets: []string{"k-123"}, keep: []string{"X-Api-Key:"}},
		{name: "x-auth-token", input: "X-Auth-Token: t-456", secrets: []string{"t-456"}},
		{name: "multiline cookies", input: "Cookie: sid=abc\nSet-Cookie: refresh=secret; Path=/\nX-Trace-Id: trace-1", secrets: []string{"sid=abc", "refresh=secret"}, keep: []string{"X-Trace-Id: trace-1"}},
		{name: "single line headers", input: "Authorization: Bearer abc Cookie: sid=secret token=secret", secrets: []string{"abc", "sid=secret", "token=secret"}},
		{name: "query credentials", input: "GET https://api.example.test/s?q=book&apikey=k1&access_token=t1&password=p1&key=k2#frag", secrets: []string{"k1", "t1", "p1", "k2"}, keep: []string{"q=book", "#frag"}},
		{name: "api_key and secret", input: "api_key=a1 client_secret=c1 searchKey=book", secrets: []string{"a1", "c1"}, keep: []string{"searchKey=book"}},
		{name: "url userinfo", input: "dial http://user:pa55@proxy.example.test:8080/ failed", secrets: []string{"pa55"}, keep: []string{"proxy.example.test:8080"}},
		{name: "plain text untouched", input: "search completed in 12ms", keep: []string{"search completed in 12ms"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactString(tt.input)
			for _, secret := range tt.secrets {
				if strings.Contains(got, secret) {
					t.Fatalf("RedactString(%q) = %q, still contains %q", tt.input, got, secret)
				}
			}
			for _, keep := range tt.keep {
				if !strings.Contains(got, keep) {
					t.Fatalf("RedactString(%q) = %q, lost %q", tt.input, got, keep)
				}
			}
		})
	}
}
