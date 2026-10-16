package urlrule

import (
	"testing"

	"reader/internal/domain/fetch"
)

func TestLooksLikeFormBody(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{body: "", want: false},
		{body: "  ", want: false},
		{body: `{"a":1}`, want: false},
		{body: `[1,2]`, want: false},
		{body: `<xml/>`, want: false},
		{body: "a=1&b=2", want: true},
		{body: "  a=1  ", want: true},
		{body: "chapterId=1", want: true},
	}
	for _, tt := range tests {
		if got := LooksLikeFormBody(tt.body); got != tt.want {
			t.Fatalf("LooksLikeFormBody(%q) = %v, want %v", tt.body, got, tt.want)
		}
	}
}

func TestApplyRetryOption(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  int
	}{
		{name: "float", value: float64(3), want: 3},
		{name: "int", value: 4, want: 4},
		{name: "int64", value: int64(5), want: 5},
		{name: "string", value: "6", want: 6},
		{name: "blank string", value: " ", want: 0},
		{name: "invalid string", value: "x", want: 0},
		{name: "non positive", value: float64(0), want: 0},
		{name: "bool ignored", value: true, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := fetch.Request{}
			ApplyRetryOption(&req, tt.value)
			if req.Retry != tt.want {
				t.Fatalf("Retry = %d, want %d", req.Retry, tt.want)
			}
		})
	}
}
