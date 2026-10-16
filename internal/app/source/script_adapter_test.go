package sourceapp

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"reader/internal/domain/book"
	domainsource "reader/internal/domain/source"
	"reader/internal/infra/script"
)

// TestScriptAdapterBindings 覆盖共享 scriptAdapter 在四条链路上的绑定（R-125）：
// 绑定键集合恒定，差异只体现在取值上，各链路不再各写一份实现。
func TestScriptAdapterBindings(t *testing.T) {
	src := &domainsource.BookSource{BookSourceURL: "https://source.test", BookSourceName: "Source"}
	bookValue := &book.Book{Name: "书名", BookURL: "https://book.test/1"}
	searchBook := &book.SearchBook{Name: "搜索结果", BookURL: "https://book.test/2"}
	chapter := &book.BookChapter{Title: "第二章", Index: 1}
	// 未注入章节时绑定值是 *book.BookChapter 的 nil（goja 里为 null），不是未定义。
	var noChapter *book.BookChapter
	// 未注入书源时绑定值是 *domainsource.BookSource 的 nil。
	var noSource *domainsource.BookSource

	tests := []struct {
		name        string
		adapter     ScriptAdapter
		wantBook    any
		wantChapter any
		wantTitle   any
		wantNext    any
		wantSrc     any
		wantSource  any
		wantBaseURL string
	}{
		{
			name:        "search list chain",
			adapter:     ScriptAdapter{BaseURL: "https://source.test/search", Source: src, Src: "list-body"},
			wantBook:    nil,
			wantChapter: noChapter,
			wantTitle:   nil,
			wantNext:    "",
			wantSrc:     "list-body",
			wantSource:  src,
			wantBaseURL: "https://source.test/search",
		},
		{
			name:        "search item chain",
			adapter:     ScriptAdapter{BaseURL: "https://source.test/search", Source: src, Src: "item-body", Book: &searchBook},
			wantBook:    &searchBook,
			wantChapter: noChapter,
			wantTitle:   nil,
			wantNext:    "",
			wantSrc:     "item-body",
			wantSource:  src,
			wantBaseURL: "https://source.test/search",
		},
		{
			name:        "book info chain",
			adapter:     ScriptAdapter{BaseURL: "https://book.test/1", Source: src, Src: "info-body", Book: bookValue, Chapter: chapter},
			wantBook:    bookValue,
			wantChapter: chapter,
			wantTitle:   "第二章",
			wantNext:    "",
			wantSrc:     "info-body",
			wantSource:  src,
			wantBaseURL: "https://book.test/1",
		},
		{
			name:        "content chain",
			adapter:     ScriptAdapter{BaseURL: "https://book.test/c/1", Source: src, Src: "content-body", Book: bookValue, Chapter: chapter, NextChapterURL: "https://book.test/c/2"},
			wantBook:    bookValue,
			wantChapter: chapter,
			wantTitle:   "第二章",
			wantNext:    "https://book.test/c/2",
			wantSrc:     "content-body",
			wantSource:  src,
			wantBaseURL: "https://book.test/c/1",
		},
		{
			name:        "content replace chain without book",
			adapter:     ScriptAdapter{BaseURL: "https://book.test/c/1"},
			wantBook:    nil,
			wantChapter: noChapter,
			wantTitle:   nil,
			wantNext:    "",
			wantSrc:     nil,
			wantSource:  noSource,
			wantBaseURL: "https://book.test/c/1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bindings := tt.adapter.Bindings("input")
			wantKeys := []string{"baseUrl", "book", "chapter", "nextChapterUrl", "result", "source", "src", "title"}
			keys := make([]string, 0, len(bindings))
			for key := range bindings {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			if strings.Join(keys, ",") != strings.Join(wantKeys, ",") {
				t.Fatalf("bindings keys = %v, want %v", keys, wantKeys)
			}
			if bindings["book"] != tt.wantBook {
				t.Fatalf("book = %#v, want %#v", bindings["book"], tt.wantBook)
			}
			if bindings["chapter"] != tt.wantChapter {
				t.Fatalf("chapter = %#v, want %#v", bindings["chapter"], tt.wantChapter)
			}
			if bindings["title"] != tt.wantTitle {
				t.Fatalf("title = %#v, want %#v", bindings["title"], tt.wantTitle)
			}
			if bindings["nextChapterUrl"] != tt.wantNext {
				t.Fatalf("nextChapterUrl = %#v, want %#v", bindings["nextChapterUrl"], tt.wantNext)
			}
			if bindings["src"] != tt.wantSrc {
				t.Fatalf("src = %#v, want %#v", bindings["src"], tt.wantSrc)
			}
			if bindings["baseUrl"] != tt.wantBaseURL {
				t.Fatalf("baseUrl = %#v, want %#v", bindings["baseUrl"], tt.wantBaseURL)
			}
			if bindings["source"] != tt.wantSource {
				t.Fatalf("source = %#v, want %#v", bindings["source"], tt.wantSource)
			}
			if bindings["result"] != "input" {
				t.Fatalf("result = %#v, want input", bindings["result"])
			}
		})
	}
}

// TestScriptAdapterEvalInjectsBindings 走真实 goja 运行时，证明共享 adapter 注入的绑定在脚本里可见。
func TestScriptAdapterEvalInjectsBindings(t *testing.T) {
	adapter := ScriptAdapter{
		Runtime:        script.NewRuntime(script.Policy{Timeout: time.Second}),
		BaseURL:        "https://book.test/c/1",
		Source:         &domainsource.BookSource{BookSourceURL: "https://source.test"},
		Src:            "content-body",
		Book:           &book.Book{Name: "书名"},
		Chapter:        &book.BookChapter{Title: "第二章"},
		NextChapterURL: "https://book.test/c/2",
	}
	got, err := adapter.Eval(context.Background(), `[src, baseUrl, book.name, chapter.title, title, nextChapterUrl].join("|")`, "ignored")
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	want := "content-body|https://book.test/c/1|书名|第二章|第二章|https://book.test/c/2"
	if got != want {
		t.Fatalf("Eval() = %#v, want %q", got, want)
	}
}

func TestScriptAdapterEvalWithoutRuntime(t *testing.T) {
	_, err := ScriptAdapter{}.Eval(context.Background(), "1", nil)
	if err == nil {
		t.Fatal("Eval() without runtime must fail")
	}
}
