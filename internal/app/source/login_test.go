package sourceapp

import (
	"context"
	"errors"
	"testing"
	"time"

	domainsource "reader/internal/domain/source"
	"reader/internal/infra/script"
	"reader/internal/infra/sqlite/repositories"
)

func TestLoginServicePersistsLoginInfoAndHeader(t *testing.T) {
	ctx := context.Background()
	db := openImporterTestDB(t)
	repo := repositories.NewBookSourceRepository(db)
	variables := repositories.NewSourceVariableRepository(db)
	service := NewLoginService(repo, NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: time.Second, Variables: variables})))

	src := &domainsource.BookSource{
		BookSourceURL:  "https://login.example.test/source",
		BookSourceName: "Login Source",
		LoginUI:        `[{"name":"username","type":"text"}]`,
		LoginURL:       `@js:function login() { var info = source.getLoginInfoMap(); source.putLoginHeader(JSON.stringify({Cookie: "sid=" + info.username})); }`,
	}
	if err := src.Normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if err := repo.Save(ctx, src); err != nil {
		t.Fatalf("save: %v", err)
	}

	state, err := service.Login(ctx, src.SourceID, map[string]string{"username": "u1"})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if !state.LoggedIn || state.LoginInfo["username"] != "u1" || state.LoginHeader["Cookie"] != "sid=u1" {
		t.Fatalf("state = %#v", state)
	}

	// 另起一个运行时实例读取，确认状态持久化在 source_variables 而不是进程内。
	fresh := NewLoginService(repo, NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: time.Second, Variables: variables})))
	reloaded, err := fresh.State(ctx, src.SourceID)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if !reloaded.LoggedIn || reloaded.LoginHeader["Cookie"] != "sid=u1" {
		t.Fatalf("reloaded = %#v", reloaded)
	}

	if err := service.Clear(ctx, src.SourceID); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	cleared, err := service.State(ctx, src.SourceID)
	if err != nil {
		t.Fatalf("State() after clear error = %v", err)
	}
	if cleared.LoggedIn || cleared.LoginInfo != nil || cleared.LoginHeader != nil {
		t.Fatalf("cleared = %#v", cleared)
	}
}

func TestLoginServiceEmptyValuesDropLoginInfo(t *testing.T) {
	ctx := context.Background()
	repo := repositories.NewBookSourceRepository(openImporterTestDB(t))
	service := NewLoginService(repo, NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: time.Second})))

	src := &domainsource.BookSource{
		BookSourceURL:  "https://login.example.test/empty",
		BookSourceName: "Login Source",
		LoginUI:        `[{"name":"username","type":"text"}]`,
		LoginURL:       `@js:function login() { throw("login must not run for empty values"); }`,
	}
	if err := src.Normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if err := repo.Save(ctx, src); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := service.Login(ctx, src.SourceID, map[string]string{"username": "u1"}); err == nil {
		t.Fatal("Login() with throwing script error = nil, want error")
	}
	state, err := service.Login(ctx, src.SourceID, nil)
	if err != nil {
		t.Fatalf("Login() with empty values error = %v", err)
	}
	if state.LoggedIn || state.LoginInfo != nil {
		t.Fatalf("state = %#v", state)
	}
}

func TestLoginServiceRejectsWebViewFlowAndMissingURL(t *testing.T) {
	ctx := context.Background()
	repo := repositories.NewBookSourceRepository(openImporterTestDB(t))
	service := NewLoginService(repo, NewScriptIntegration(script.NewRuntime(script.Policy{Timeout: time.Second})))

	webview := &domainsource.BookSource{
		BookSourceURL:  "https://login.example.test/webview",
		BookSourceName: "WebView Source",
		LoginURL:       "https://login.example.test/signin",
	}
	if err := webview.Normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if err := repo.Save(ctx, webview); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := service.Login(ctx, webview.SourceID, nil); !errors.Is(err, ErrUnsupportedLoginFlow) {
		t.Fatalf("Login() error = %v, want ErrUnsupportedLoginFlow", err)
	}

	noURL := &domainsource.BookSource{
		BookSourceURL:  "https://login.example.test/no-url",
		BookSourceName: "No URL Source",
		LoginUI:        `[{"name":"username","type":"text"}]`,
	}
	if err := noURL.Normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if err := repo.Save(ctx, noURL); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := service.Login(ctx, noURL.SourceID, map[string]string{"username": "u"}); !errors.Is(err, ErrLoginURLMissing) {
		t.Fatalf("Login() error = %v, want ErrLoginURLMissing", err)
	}
}
