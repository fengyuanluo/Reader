package script

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"reader/internal/domain/source"
)

// newTestSource 构造带 jsLib 的书源，用于跨源隔离测试。
func newTestSource(url, jsLib string) *source.BookSource {
	return &source.BookSource{BookSourceURL: url, JSLib: jsLib}
}

// 相同脚本重复 Eval：缓存命中路径必须返回一致结果。
func TestRuntimeRepeatedEvalUsesCachedProgram(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	for i := range 3 {
		got, err := runtime.Eval(context.Background(), `result + ":" + (1 + 2)`, "hit")
		if err != nil {
			t.Fatalf("Eval() #%d error = %v", i, err)
		}
		if got != "hit:3" {
			t.Fatalf("Eval() #%d = %#v", i, got)
		}
	}
	if runtime.callSites.len() != 1 {
		t.Fatalf("callSites cache len = %d, want 1", runtime.callSites.len())
	}
	first, _ := runtime.callSites.compileScript("eval.main", `result + ":" + (1 + 2)`)
	second, _ := runtime.callSites.compileScript("eval.main", `result + ":" + (1 + 2)`)
	if first != second {
		t.Fatal("identical script recompiled")
	}
	// 相同文本、不同调用点不能命中同一缓存（绑定语义不同）。
	_, err := runtime.callSites.compileScript("other.site", `result + ":" + (1 + 2)`)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.callSites.len() != 2 {
		t.Fatalf("same text at another site should compile separately, len = %d", runtime.callSites.len())
	}
}

// 超时之后的下一次 Eval 必须正常：不继承中断状态，也不复用被中断的 VM。
func TestRuntimeEvalAfterTimeoutIsClean(t *testing.T) {
	runtime := NewRuntime(Policy{Timeout: 20 * time.Millisecond})
	if _, err := runtime.Eval(context.Background(), `while (true) {}`, nil); !errors.Is(err, ErrScriptTimeout) {
		t.Fatalf("error = %v, want ErrScriptTimeout", err)
	}
	got, err := runtime.Eval(context.Background(), `1 + 1`, nil)
	if err != nil {
		t.Fatalf("Eval after timeout error = %v", err)
	}
	if got != int64(2) {
		t.Fatalf("got = %#v, want 2", got)
	}
	// 超时的 VM 不放回池：连续多次超时后再 Eval 依旧正常。
	for range 3 {
		if _, err := runtime.Eval(context.Background(), `while (true) {}`, nil); !errors.Is(err, ErrScriptTimeout) {
			t.Fatalf("timeout error = %v", err)
		}
		if _, err := runtime.Eval(context.Background(), `"ok"`, nil); err != nil {
			t.Fatalf("Eval after timeout error = %v", err)
		}
	}
}

// 两个不同书源交替 Eval：变量、缓存与全局状态不得串。
func TestRuntimeAlternatingSourcesDoNotLeak(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	srcA := newTestSource("https://source-a.example.test/root", `var marker = "A"; function markerOf(){ return marker; }`)
	srcB := newTestSource("https://source-b.example.test/root", `var marker = "B"; function markerOf(){ return marker; }`)

	for range 5 {
		gotA, err := runtime.EvalWithBindings(context.Background(), `source.put("token", "A1"); markerOf()`, Bindings{"source": srcA})
		if err != nil || gotA != "A" {
			t.Fatalf("source-a Eval = %#v, %v", gotA, err)
		}
		gotB, err := runtime.EvalWithBindings(context.Background(), `source.put("token", "B1"); markerOf()`, Bindings{"source": srcB})
		if err != nil || gotB != "B" {
			t.Fatalf("source-b Eval = %#v, %v", gotB, err)
		}
	}
	// jsLib 泄漏的全局 marker 不得跨 Eval 留存。
	got, err := runtime.Eval(context.Background(), `typeof marker === "undefined"`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != true {
		t.Fatalf("global marker leaked between Evals: %v", got)
	}
	// source.put 的状态按书源隔离。
	gotA, err := runtime.EvalWithBindings(context.Background(), `source.get("token")`, Bindings{"source": srcA})
	if err != nil || gotA != "A1" {
		t.Fatalf("source-a token = %#v, %v", gotA, err)
	}
	gotB, err := runtime.EvalWithBindings(context.Background(), `source.get("token")`, Bindings{"source": srcB})
	if err != nil || gotB != "B1" {
		t.Fatalf("source-b token = %#v, %v", gotB, err)
	}
}

// 并发下不同书源交替 Eval：池化 VM 与缓存命中路径不得串数据（race 检测覆盖）。
func TestRuntimeConcurrentAlternatingSourcesDoNotLeak(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	srcA := newTestSource("https://source-a.example.test/root", `var marker = "A";`)
	srcB := newTestSource("https://source-b.example.test/root", `var marker = "B";`)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for range 4 {
				src := srcA
				want := "A"
				if i%2 == 1 {
					src = srcB
					want = "B"
				}
				got, err := runtime.EvalWithBindings(context.Background(), `marker + source.get("token")`, Bindings{"source": src})
				if err != nil {
					t.Errorf("Eval error = %v", err)
					return
				}
				// source.get("token") 未写入时为空串，首字符必须始终是本源的 marker。
				if !strings.HasPrefix(got.(string), want) {
					t.Errorf("got = %#v, want prefix %q", got, want)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

// 每源 Eval 后书源 A 对 String.prototype / Object.defineProperty 的修改不得残留到书源 B
// 的 Eval（旧池化实现只清可枚举全局，非枚举属性与 defineProperty 会跨源污染）。
func TestRuntimePrototypeMutationDoesNotLeakAcrossSources(t *testing.T) {
	runtime := NewRuntime(Policy{SourceKey: "src"})
	srcA := newTestSource("https://source-a.example.test/root", `
String.prototype.readerPoison = function() { return "poisoned"; };
Object.defineProperty(Object.prototype, "readerLeak", {
	value: function() { return "leaked"; },
	enumerable: false,
	configurable: true
});
`)
	srcB := newTestSource("https://source-b.example.test/root", "")
	if _, err := runtime.EvalWithBindings(context.Background(), `"x".readerPoison()`, Bindings{"source": srcA}); err != nil {
		t.Fatalf("source-a Eval error = %v", err)
	}
	got, err := runtime.EvalWithBindings(context.Background(), `[typeof "x".readerPoison, typeof ({}).readerLeak].join("|")`, Bindings{"source": srcB})
	if err != nil {
		t.Fatalf("source-b Eval error = %v", err)
	}
	if got != "undefined|undefined" {
		t.Fatalf("prototype mutation leaked to source-b: %v", got)
	}
	// 任意后续无源 Eval 同样干净。
	got, err = runtime.Eval(context.Background(), `typeof "x".readerPoison`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "undefined" {
		t.Fatalf("prototype mutation leaked to plain Eval: %v", got)
	}
}
