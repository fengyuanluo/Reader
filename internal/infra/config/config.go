package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
	EnvTest        = "test"

	defaultTokenForTest = "reader-test-token"
)

type Config struct {
	Addr                        string
	DataDir                     string
	SQLiteDSN                   string
	Token                       string
	Env                         string
	DefaultUserAgent            string
	FetchTimeout                time.Duration
	FetchMaxBodyBytes           int64
	FetchMaxRedirects           int
	AllowPrivateNetwork         bool
	GlobalConcurrency           int
	SourceConcurrency           int
	ScriptTimeout               time.Duration
	RegexTimeout                time.Duration
	DebugBodyLimit              int64
	ContentReSegment            bool
	ContentChineseConverterType int
	RealE2EEnabled              bool
	RealE2ESourceCollection     string
	RealE2EKeyword              string
	RealE2EMaxCandidates        int
}

type Source interface {
	Lookup(key string) (string, bool)
}

type SourceFunc func(key string) (string, bool)

func (fn SourceFunc) Lookup(key string) (string, bool) {
	return fn(key)
}

func FromEnv() Source {
	return SourceFunc(os.LookupEnv)
}

func FromMap(values map[string]string) Source {
	return SourceFunc(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
}

func Load(source Source) (Config, error) {
	cfg := Config{
		Addr:                        ":8080",
		DataDir:                     "data",
		SQLiteDSN:                   "data/reader.db",
		Env:                         EnvDevelopment,
		DefaultUserAgent:            "Reader/0.0.1",
		FetchTimeout:                30 * time.Second,
		FetchMaxBodyBytes:           10 * 1024 * 1024,
		FetchMaxRedirects:           5,
		AllowPrivateNetwork:         false,
		GlobalConcurrency:           8,
		SourceConcurrency:           2,
		ScriptTimeout:               10 * time.Second,
		RegexTimeout:                500 * time.Millisecond,
		DebugBodyLimit:              32 * 1024,
		ContentReSegment:            false,
		ContentChineseConverterType: 0,
		RealE2EEnabled:              false,
		RealE2ESourceCollection:     "https://shuyuan-api.yiove.com/import/book-source-collection/a8adf570-e115-4b99-87e3-ccaf298ae361",
		RealE2EKeyword:              "斗破苍穹",
		RealE2EMaxCandidates:        10,
	}

	var err error
	cfg.Addr = stringValue(source, "READER_ADDR", cfg.Addr)
	cfg.DataDir = stringValue(source, "READER_DATA_DIR", cfg.DataDir)
	cfg.SQLiteDSN = stringValue(source, "READER_SQLITE_DSN", cfg.SQLiteDSN)
	cfg.Token = stringValue(source, "READER_TOKEN", cfg.Token)
	cfg.Env = stringValue(source, "READER_ENV", cfg.Env)
	cfg.DefaultUserAgent = stringValue(source, "READER_DEFAULT_USER_AGENT", cfg.DefaultUserAgent)
	cfg.FetchTimeout, err = durationValue(source, "READER_FETCH_TIMEOUT", cfg.FetchTimeout)
	if err != nil {
		return Config{}, err
	}
	cfg.FetchMaxBodyBytes, err = int64Value(source, "READER_FETCH_MAX_BODY_BYTES", cfg.FetchMaxBodyBytes, 1)
	if err != nil {
		return Config{}, err
	}
	cfg.FetchMaxRedirects, err = intValue(source, "READER_FETCH_MAX_REDIRECTS", cfg.FetchMaxRedirects, 0)
	if err != nil {
		return Config{}, err
	}
	cfg.AllowPrivateNetwork, err = boolValue(source, "READER_ALLOW_PRIVATE_NETWORK", cfg.AllowPrivateNetwork)
	if err != nil {
		return Config{}, err
	}
	cfg.GlobalConcurrency, err = intValue(source, "READER_GLOBAL_CONCURRENCY", cfg.GlobalConcurrency, 1)
	if err != nil {
		return Config{}, err
	}
	cfg.SourceConcurrency, err = intValue(source, "READER_SOURCE_CONCURRENCY", cfg.SourceConcurrency, 1)
	if err != nil {
		return Config{}, err
	}
	cfg.ScriptTimeout, err = durationValue(source, "READER_SCRIPT_TIMEOUT", cfg.ScriptTimeout)
	if err != nil {
		return Config{}, err
	}
	cfg.RegexTimeout, err = durationValue(source, "READER_REGEX_TIMEOUT", cfg.RegexTimeout)
	if err != nil {
		return Config{}, err
	}
	cfg.DebugBodyLimit, err = int64Value(source, "READER_DEBUG_BODY_LIMIT", cfg.DebugBodyLimit, 1)
	if err != nil {
		return Config{}, err
	}
	cfg.ContentReSegment, err = boolValue(source, "READER_CONTENT_RESEGMENT", cfg.ContentReSegment)
	if err != nil {
		return Config{}, err
	}
	cfg.ContentChineseConverterType, err = intValue(source, "READER_CONTENT_CHINESE_CONVERTER_TYPE", cfg.ContentChineseConverterType, 0)
	if err != nil {
		return Config{}, err
	}
	cfg.RealE2EEnabled, err = boolValue(source, "READER_REAL_E2E_ENABLED", cfg.RealE2EEnabled)
	if err != nil {
		return Config{}, err
	}
	cfg.RealE2ESourceCollection = stringValue(source, "READER_REAL_E2E_SOURCE_COLLECTION_URL", cfg.RealE2ESourceCollection)
	cfg.RealE2EKeyword = stringValue(source, "READER_REAL_E2E_KEYWORD", cfg.RealE2EKeyword)
	cfg.RealE2EMaxCandidates, err = intValue(source, "READER_REAL_E2E_MAX_CANDIDATES", cfg.RealE2EMaxCandidates, 1)
	if err != nil {
		return Config{}, err
	}

	if cfg.Env == EnvTest && cfg.Token == "" {
		cfg.Token = defaultTokenForTest
	}
	if cfg.Env == EnvProduction && cfg.Token == "" {
		return Config{}, errors.New("READER_TOKEN is required in production")
	}
	return cfg, nil
}

func (cfg Config) Redacted() map[string]any {
	token := ""
	if cfg.Token != "" {
		token = "[REDACTED]"
	}
	return map[string]any{
		"addr":                        cfg.Addr,
		"dataDir":                     cfg.DataDir,
		"sqliteDSN":                   cfg.SQLiteDSN,
		"token":                       token,
		"env":                         cfg.Env,
		"defaultUserAgent":            cfg.DefaultUserAgent,
		"fetchTimeout":                cfg.FetchTimeout.String(),
		"fetchMaxBodyBytes":           cfg.FetchMaxBodyBytes,
		"fetchMaxRedirects":           cfg.FetchMaxRedirects,
		"allowPrivateNetwork":         cfg.AllowPrivateNetwork,
		"globalConcurrency":           cfg.GlobalConcurrency,
		"sourceConcurrency":           cfg.SourceConcurrency,
		"scriptTimeout":               cfg.ScriptTimeout.String(),
		"regexTimeout":                cfg.RegexTimeout.String(),
		"debugBodyLimit":              cfg.DebugBodyLimit,
		"contentReSegment":            cfg.ContentReSegment,
		"contentChineseConverterType": cfg.ContentChineseConverterType,
		"realE2EEnabled":              cfg.RealE2EEnabled,
		"realE2ESourceCollection":     cfg.RealE2ESourceCollection,
		"realE2EKeyword":              cfg.RealE2EKeyword,
		"realE2EMaxCandidates":        cfg.RealE2EMaxCandidates,
	}
}

func stringValue(source Source, key string, fallback string) string {
	if value, ok := source.Lookup(key); ok {
		return value
	}
	return fallback
}

func boolValue(source Source, key string, fallback bool) (bool, error) {
	value, ok := source.Lookup(key)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return parsed, nil
}

func durationValue(source Source, key string, fallback time.Duration) (time.Duration, error) {
	value, ok := source.Lookup(key)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return parsed, nil
}

func intValue(source Source, key string, fallback int, min int) (int, error) {
	value, ok := source.Lookup(key)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if parsed < min {
		return 0, fmt.Errorf("%s must be >= %d", key, min)
	}
	return parsed, nil
}

func int64Value(source Source, key string, fallback int64, min int64) (int64, error) {
	value, ok := source.Lookup(key)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if parsed < min {
		return 0, fmt.Errorf("%s must be >= %d", key, min)
	}
	return parsed, nil
}
