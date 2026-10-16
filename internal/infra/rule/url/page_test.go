package urlrule

import "testing"

func TestReplacePagePlaceholders(t *testing.T) {
	tests := []struct {
		input string
		page  int
		want  string
	}{
		{"https://x.test/list?page=<1,2,3>", 1, "https://x.test/list?page=1"},
		{"https://x.test/list?page=<1,2,3>", 2, "https://x.test/list?page=2"},
		{"https://x.test/list?page=<1,2,3>", 3, "https://x.test/list?page=3"},
		{"https://x.test/list?page=<1,2,3>", 9, "https://x.test/list?page=3"},
		{"https://x.test/list?page=<1,2,3>", 0, "https://x.test/list?page=1"},
		{"https://x.test/<a,b>/?p=<x,y>", 2, "https://x.test/b/?p=y"},
		{"https://x.test/list", 2, "https://x.test/list"},
		{"https://x.test/list?p=< 1 , 2 >", 2, "https://x.test/list?p=2"},
	}
	for _, tt := range tests {
		if got := ReplacePagePlaceholders(tt.input, tt.page); got != tt.want {
			t.Fatalf("ReplacePagePlaceholders(%q, %d) = %q, want %q", tt.input, tt.page, got, tt.want)
		}
	}
}
