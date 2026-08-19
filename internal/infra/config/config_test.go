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

	if cfg.Addr != ":8080" {
		t.Fatalf("Addr = %q, want :8080", cfg.Addr)
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
	if cfg.RealE2EEnabled {
		t.Fatal("RealE2EEnabled default = true, want false")
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
		"READER_REGEX_TIMEOUT":                  "750ms",
		"READER_DEBUG_BODY_LIMIT":               "4096",
		"READER_CONTENT_RESEGMENT":              "true",
		"READER_CONTENT_CHINESE_CONVERTER_TYPE": "2",
		"READER_REAL_E2E_ENABLED":               "true",
		"READER_REAL_E2E_KEYWORD":               "测试",
		"READER_REAL_E2E_MAX_CANDIDATES":        "5",
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
	if cfg.RealE2EKeyword != "测试" {
		t.Fatalf("RealE2EKeyword = %q, want 测试", cfg.RealE2EKeyword)
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
	}

	for _, env := range tests {
		if _, err := Load(FromMap(env)); err == nil {
			t.Fatalf("Load(%v) error = nil, want error", env)
		}
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
