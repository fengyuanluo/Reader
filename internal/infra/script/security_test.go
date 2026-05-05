package script

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRuntimeInterruptsInfiniteLoop(t *testing.T) {
	runtime := NewRuntime(Policy{Timeout: 10 * time.Millisecond, Stage: "rule_js"})
	_, err := runtime.Eval(context.Background(), `while (true) {}`, nil)
	if !errors.Is(err, ErrScriptTimeout) {
		t.Fatalf("error = %v, want ErrScriptTimeout", err)
	}
	if !strings.Contains(err.Error(), "rule_js") {
		t.Fatalf("error = %v, want stage", err)
	}
}

func TestRuntimeCacheIsNamespacedBySource(t *testing.T) {
	cache := NewNamespacedCache()
	first := NewRuntime(Policy{SourceKey: "source-a"})
	first.cache = cache
	second := NewRuntime(Policy{SourceKey: "source-b"})
	second.cache = cache

	if _, err := first.Eval(context.Background(), `cache.put("token", "a"); "ok"`, nil); err != nil {
		t.Fatalf("first Eval() error = %v", err)
	}
	got, err := second.Eval(context.Background(), `cache.get("token") || "missing"`, nil)
	if err != nil {
		t.Fatalf("second Eval() error = %v", err)
	}
	if got != "missing" {
		t.Fatalf("got = %#v, want missing", got)
	}
}

func TestRuntimeSafeBindingsLogWithoutLeakingSecrets(t *testing.T) {
	runtime := NewRuntime(Policy{AllowLog: true, BaseURL: "https://example.test/book/index.html"})
	got, err := runtime.Eval(context.Background(), `log("Cookie: sid=secret"); "../c/1.html"`, nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	if got != "../c/1.html" {
		t.Fatalf("got = %#v", got)
	}
	logs := runtime.Logs()
	if len(logs) != 1 || strings.Contains(logs[0], "sid=secret") {
		t.Fatalf("logs = %#v", logs)
	}
}

func TestRuntimeErrorIsRedacted(t *testing.T) {
	runtime := NewRuntime(Policy{Stage: "login_check_js"})
	_, err := runtime.Eval(context.Background(), `throw new Error("Set-Cookie: sid=secret")`, nil)
	if !errors.Is(err, ErrScriptFailed) {
		t.Fatalf("error = %v, want ErrScriptFailed", err)
	}
	if strings.Contains(err.Error(), "sid=secret") || !strings.Contains(err.Error(), "login_check_js") {
		t.Fatalf("error = %v", err)
	}
}
