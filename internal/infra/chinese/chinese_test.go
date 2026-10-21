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

// S09-06：词典加载失败不得永久缓存——坏模式首次失败返回原文，修复后下次调用重试成功。
func TestConverterFailureRetries(t *testing.T) {
	c := &converter{mode: "no-such-dictionary-mode"}
	if got, err := c.convert("繁體"); err == nil || got != "繁體" {
		t.Fatalf("convert with bad mode = %q, err=%v; want original + error", got, err)
	}
	// "修复"词典（换成合法模式）后必须能恢复，证明失败未永久缓存。
	c.mode = "t2s"
	if got, err := c.convert("繁體"); err != nil || got != "繁体" {
		t.Fatalf("convert after fixing mode = %q, err=%v; want 繁体", got, err)
	}
}
