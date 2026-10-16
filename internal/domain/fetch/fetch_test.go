package fetch

import "testing"

// TestHeadersGet 覆盖 domain 自有响应头类型的取值语义（R-098）：键按 MIME 规范形式匹配，
// 首值为空或键不存在时返回空串，与 net/http.Header.Get 一致。
func TestHeadersGet(t *testing.T) {
	tests := []struct {
		name    string
		headers Headers
		key     string
		want    string
	}{
		{name: "nil headers", headers: nil, key: "Content-Type", want: ""},
		{name: "canonical key", headers: Headers{"Content-Type": {"text/html"}}, key: "Content-Type", want: "text/html"},
		{name: "lowercase query key", headers: Headers{"Content-Type": {"text/html"}}, key: "content-type", want: "text/html"},
		{name: "first value", headers: Headers{"Set-Cookie": {"a=1", "b=2"}}, key: "Set-Cookie", want: "a=1"},
		{name: "empty values", headers: Headers{"Content-Type": {}}, key: "Content-Type", want: ""},
		{name: "missing key", headers: Headers{"Content-Type": {"text/html"}}, key: "X-Trace", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.headers.Get(tt.key); got != tt.want {
				t.Fatalf("Get(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}
