package chinese

import (
	"sync"
	"testing"
)

func TestConvert(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string) string
		in   string
		want string
	}{
		{name: "t2s", fn: T2S, in: "繁體中文", want: "繁体中文"},
		{name: "s2t", fn: S2T, in: "简体中文", want: "簡體中文"},
		{name: "t2s empty", fn: T2S, in: "", want: ""},
		{name: "s2t ascii", fn: S2T, in: "abc 123", want: "abc 123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fn(tt.in); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// 并发首次调用必须只初始化一次词典且无数据竞争（配合 -race）。
func TestConvertConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if got := T2S("書"); got != "书" {
				t.Errorf("T2S = %q", got)
			}
		}()
		go func() {
			defer wg.Done()
			if got := S2T("书"); got != "書" {
				t.Errorf("S2T = %q", got)
			}
		}()
	}
	wg.Wait()
}
