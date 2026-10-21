package config

import (
	"testing"
	"time"
)

func TestLoadUsesSafeDefaultsInTestEnvironment(t *testing.T) {
	cfg, err := Load(FromMap(map[string]string{
		"READER_ENV": "test",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Addr != "127.0.0.1:8080" {
		t.Fatalf("Addr = %q, want 127.0.0.1:8080", cfg.Addr)
	}
	if cfg.Token == "" {
		t.Fatal("Token is empty in test environment")
	}
	if cfg.FetchTimeout != 30*time.Second {
		t.Fatalf("FetchTimeout = %s, want 30s", cfg.FetchTimeout)
	}
	if cfg.DefaultUserAgent != "Reader/0.1.0" {
		t.Fatalf("DefaultUserAgent = %q, want Reader/0.1.0", cfg.DefaultUserAgent)
	}
	if cfg.FetchMaxBodyBytes != 10*1024*1024 {
		t.Fatalf("FetchMaxBodyBytes = %d, want 10485760", cfg.FetchMaxBodyBytes)
	}
}

func TestLoadRejectsProductionWithoutToken(t *testing.T) {
	_, err := Load(FromMap(map[string]string{
		"READER_ENV": "production",
	}))
	if err == nil {
		t.Fatal("Load() error = nil, want missing token error")
	}
}

func TestLoadAcceptsProductionWithTokenAndOverrides(t *testing.T) {
	cfg, err := Load(FromMap(map[string]string{
		"READER_ADDR":                           ":9090",
		"READER_DATA_DIR":                       "/var/lib/reader",
		"READER_SQLITE_DSN":                     "/var/lib/reader/reader.db",
		"READER_TOKEN":                          "secret-token",
		"READER_ENV":                            "production",
		"READER_FETCH_TIMEOUT":                  "12s",
		"READER_FETCH_MAX_BODY_BYTES":           "2048",
		"READER_FETCH_MAX_REDIRECTS":            "3",
		"READER_ALLOW_PRIVATE_NETWORK":          "true",
		"READER_GLOBAL_CONCURRENCY":             "8",
		"READER_SOURCE_CONCURRENCY":             "2",
		"READER_SCRIPT_TIMEOUT":                 "1500ms",
		"READER_DEBUG_BODY_LIMIT":               "4096",
		"READER_CONTENT_RESEGMENT":              "true",
		"READER_CONTENT_CHINESE_CONVERTER_TYPE": "2",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Addr != ":9090" {
		t.Fatalf("Addr = %q, want :9090", cfg.Addr)
	}
	if cfg.DataDir != "/var/lib/reader" {
		t.Fatalf("DataDir = %q", cfg.DataDir)
	}
	if cfg.SQLiteDSN != "/var/lib/reader/reader.db" {
		t.Fatalf("SQLiteDSN = %q", cfg.SQLiteDSN)
	}
	if cfg.Token != "secret-token" {
		t.Fatal("Token override was not loaded")
	}
	if cfg.FetchTimeout != 12*time.Second {
		t.Fatalf("FetchTimeout = %s, want 12s", cfg.FetchTimeout)
	}
	if cfg.FetchMaxRedirects != 3 {
		t.Fatalf("FetchMaxRedirects = %d, want 3", cfg.FetchMaxRedirects)
	}
	if !cfg.AllowPrivateNetwork {
		t.Fatal("AllowPrivateNetwork = false, want true")
	}
	if !cfg.ContentReSegment {
		t.Fatal("ContentReSegment = false, want true")
	}
	if cfg.ContentChineseConverterType != 2 {
		t.Fatalf("ContentChineseConverterType = %d, want 2", cfg.ContentChineseConverterType)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []map[string]string{
		{"READER_ENV": "test", "READER_FETCH_TIMEOUT": "bad"},
		{"READER_ENV": "test", "READER_FETCH_MAX_BODY_BYTES": "0"},
		{"READER_ENV": "test", "READER_GLOBAL_CONCURRENCY": "-1"},
		{"READER_ENV": "test", "READER_ALLOW_PRIVATE_NETWORK": "not-bool"},
		{"READER_ENV": "test", "READER_CONTENT_CHINESE_CONVERTER_TYPE": "-1"},
		// S09-03：ConverterType 无上限校验（值 ≥3 静默返回原文）。
		{"READER_ENV": "test", "READER_CONTENT_CHINESE_CONVERTER_TYPE": "3"},
		// S09-03：READER_ENV 校验合法取值集合。
		{"READER_ENV": "staging"},
		{"READER_ENV": ""},
		// GoMemLimitRatio 边界。
		{"READER_ENV": "test", "READER_GOMEM_LIMIT_RATIO": "0"},
		{"READER_ENV": "test", "READER_GOMEM_LIMIT_RATIO": "1.5"},
		{"READER_ENV": "test", "READER_GOMEM_LIMIT_RATIO": "not-a-number"},
	}

	for _, env := range tests {
		if _, err := Load(FromMap(env)); err == nil {
			t.Fatalf("Load(%v) error = nil, want error", env)
		}
	}
}

// S09-03：READER_ENV=Production（大写 P）此前静默落入 development 分支，必须报错。
func TestLoadRejectsWrongCaseEnv(t *testing.T) {
	if _, err := Load(FromMap(map[string]string{"READER_ENV": "Production"})); err == nil {
		t.Fatal("Load(Production) error = nil, want error")
	}
}

// 契约 3：READER_SEARCH_SOURCE_TIMEOUT 覆盖多源搜索/校验的每源默认超时，默认 15s。
func TestLoadSearchSourceTimeout(t *testing.T) {
	cfg, err := Load(FromMap(map[string]string{"READER_ENV": "test"}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SearchSourceTimeout != 15*time.Second {
		t.Fatalf("SearchSourceTimeout = %s, want 15s", cfg.SearchSourceTimeout)
	}
	cfg, err = Load(FromMap(map[string]string{
		"READER_ENV":                   "test",
		"READER_SEARCH_SOURCE_TIMEOUT": "8s",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SearchSourceTimeout != 8*time.Second {
		t.Fatalf("SearchSourceTimeout = %s, want 8s", cfg.SearchSourceTimeout)
	}
	if redacted, ok := cfg.Redacted()["searchSourceTimeout"]; !ok || redacted != "8s" {
		t.Fatalf("Redacted()[searchSourceTimeout] = %v (%T), want \"8s\"", redacted, redacted)
	}
}

func TestLoadReadsConcurrencyAndDropsRegexTimeout(t *testing.T) {
	cfg, err := Load(FromMap(map[string]string{
		"READER_ENV":                "test",
		"READER_GLOBAL_CONCURRENCY": "16",
		"READER_SOURCE_CONCURRENCY": "3",
		"READER_REGEX_TIMEOUT":      "750ms",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GlobalConcurrency != 16 || cfg.SourceConcurrency != 3 {
		t.Fatalf("concurrency = %d/%d, want 16/3", cfg.GlobalConcurrency, cfg.SourceConcurrency)
	}
	if _, ok := cfg.Redacted()["regexTimeout"]; ok {
		t.Fatal("Redacted() still exposes the removed regexTimeout option")
	}
}

func TestLoadReadsCheckConcurrency(t *testing.T) {
	cfg, err := Load(FromMap(map[string]string{
		"READER_ENV":               "test",
		"READER_CHECK_CONCURRENCY": "24",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CheckConcurrency != 24 {
		t.Fatalf("CheckConcurrency = %d, want 24", cfg.CheckConcurrency)
	}

	defaultCfg, err := Load(FromMap(map[string]string{"READER_ENV": "test"}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if defaultCfg.CheckConcurrency != 16 {
		t.Fatalf("default CheckConcurrency = %d, want 16", defaultCfg.CheckConcurrency)
	}
}

func TestRedactedDoesNotExposeToken(t *testing.T) {
	cfg, err := Load(FromMap(map[string]string{
		"READER_ENV":   "production",
		"READER_TOKEN": "secret-token",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	redacted := cfg.Redacted()
	if redacted["token"] == "secret-token" {
		t.Fatal("redacted config exposed token")
	}
	if redacted["token"] != "[REDACTED]" {
		t.Fatalf("redacted token = %v, want [REDACTED]", redacted["token"])
	}
	if _, ok := redacted["contentReSegment"]; !ok {
		t.Fatal("redacted config missing contentReSegment")
	}
	if _, ok := redacted["contentChineseConverterType"]; !ok {
		t.Fatal("redacted config missing contentChineseConverterType")
	}
}

func TestLoadDefaultAddrIsLoopback(t *testing.T) {
	cfg, err := Load(FromMap(map[string]string{}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Addr != "127.0.0.1:8080" || cfg.Token != "" {
		t.Fatalf("defaults addr=%q token=%q, want 127.0.0.1:8080 and empty token", cfg.Addr, cfg.Token)
	}
	if err := RequireTokenForAddr(cfg.Addr, cfg.Token); err != nil {
		t.Fatalf("default config must be startable without token: %v", err)
	}
}

func TestRequireTokenForAddr(t *testing.T) {
	tests := []struct {
		addr    string
		token   string
		wantErr bool
	}{
		{addr: "127.0.0.1:8080"},
		{addr: "127.1.2.3:8080"},
		{addr: "[::1]:8080"},
		{addr: "localhost:8080"},
		{addr: "LOCALHOST:1"},
		{addr: ":8080", wantErr: true},
		{addr: "0.0.0.0:8080", wantErr: true},
		{addr: "[::]:8080", wantErr: true},
		{addr: "192.168.1.2:8080", wantErr: true},
		{addr: "reader.example:8080", wantErr: true},
		{addr: "127.0.0.1", wantErr: true},
		{addr: ":8080", token: "secret"},
		{addr: "0.0.0.0:8080", token: "secret"},
	}
	for _, tt := range tests {
		t.Run(tt.addr+"/"+tt.token, func(t *testing.T) {
			err := RequireTokenForAddr(tt.addr, tt.token)
			if (err != nil) != tt.wantErr {
				t.Fatalf("RequireTokenForAddr(%q, %q) error = %v, wantErr %v", tt.addr, tt.token, err, tt.wantErr)
			}
		})
	}
}

// TestLoadDerivesSQLiteDSNFromDataDir 覆盖 READER_DATA_DIR 与默认 SQLite 路径的联动：
// 未显式设置 READER_SQLITE_DSN 时，数据库默认落在数据目录内（默认仍为 data/reader.db）；
// 显式设置的 DSN 优先级最高。
func TestLoadDerivesSQLiteDSNFromDataDir(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "defaults", env: map[string]string{}, want: "data/reader.db"},
		{name: "data dir only", env: map[string]string{"READER_DATA_DIR": "/var/lib/reader"}, want: "/var/lib/reader/reader.db"},
		{name: "explicit dsn wins", env: map[string]string{"READER_DATA_DIR": "/var/lib/reader", "READER_SQLITE_DSN": "/mnt/db/reader.db"}, want: "/mnt/db/reader.db"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(FromMap(tc.env))
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.SQLiteDSN != tc.want {
				t.Fatalf("SQLiteDSN = %q, want %q", cfg.SQLiteDSN, tc.want)
			}
		})
	}
}
