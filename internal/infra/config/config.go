package config

import (
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	CheckConcurrency            int
	ScriptTimeout               time.Duration
	DebugBodyLimit              int64
	ContentReSegment            bool
	ContentChineseConverterType int
	// SearchSourceTimeout 是多源搜索/校验的每源默认超时；Timeout<=0 时生效。
	SearchSourceTimeout time.Duration
	// GoMemLimitRatio 控制 serve 启动时按可用内存设置的软内存上限比例（0.1~1，默认 0.5）。
	GoMemLimitRatio float64
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
		Addr:                        "127.0.0.1:8080",
		DataDir:                     "data",
		Env:                         EnvDevelopment,
		DefaultUserAgent:            "Reader/0.1.0",
		FetchTimeout:                30 * time.Second,
		FetchMaxBodyBytes:           10 * 1024 * 1024,
		FetchMaxRedirects:           5,
		AllowPrivateNetwork:         false,
		GlobalConcurrency:           8,
		SourceConcurrency:           2,
		CheckConcurrency:            16,
		ScriptTimeout:               10 * time.Second,
		DebugBodyLimit:              32 * 1024,
		ContentReSegment:            false,
		ContentChineseConverterType: 0,
		SearchSourceTimeout:         15 * time.Second,
		GoMemLimitRatio:             0.5,
	}

	var err error
	cfg.Addr = stringValue(source, "READER_ADDR", cfg.Addr)
	cfg.DataDir = stringValue(source, "READER_DATA_DIR", cfg.DataDir)
	cfg.SQLiteDSN = dataDirSQLiteDSN(source, cfg.DataDir)
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
	cfg.FetchMaxRedirects, err = intValue(source, "READER_FETCH_MAX_REDIRECTS", cfg.FetchMaxRedirects, 0, math.MaxInt32)
	if err != nil {
		return Config{}, err
	}
	cfg.AllowPrivateNetwork, err = boolValue(source, "READER_ALLOW_PRIVATE_NETWORK", cfg.AllowPrivateNetwork)
	if err != nil {
		return Config{}, err
	}
	cfg.GlobalConcurrency, err = intValue(source, "READER_GLOBAL_CONCURRENCY", cfg.GlobalConcurrency, 1, math.MaxInt32)
	if err != nil {
		return Config{}, err
	}
	cfg.SourceConcurrency, err = intValue(source, "READER_SOURCE_CONCURRENCY", cfg.SourceConcurrency, 1, math.MaxInt32)
	if err != nil {
		return Config{}, err
	}
	cfg.CheckConcurrency, err = intValue(source, "READER_CHECK_CONCURRENCY", cfg.CheckConcurrency, 1, math.MaxInt32)
	if err != nil {
		return Config{}, err
	}
	cfg.ScriptTimeout, err = durationValue(source, "READER_SCRIPT_TIMEOUT", cfg.ScriptTimeout)
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
	cfg.ContentChineseConverterType, err = intValue(source, "READER_CONTENT_CHINESE_CONVERTER_TYPE", cfg.ContentChineseConverterType, 0, 2)
	if err != nil {
		return Config{}, err
	}
	cfg.SearchSourceTimeout, err = durationValue(source, "READER_SEARCH_SOURCE_TIMEOUT", cfg.SearchSourceTimeout)
	if err != nil {
		return Config{}, err
	}
	cfg.GoMemLimitRatio, err = floatValue(source, "READER_GOMEM_LIMIT_RATIO", cfg.GoMemLimitRatio, 0.1, 1.0)
	if err != nil {
		return Config{}, err
	}

	// READER_ENV 只接受固定取值集合；此前任意字符串（含大小写差异，如 Production）会静默落入
	// development 分支，回环部署时鉴权被静默关闭且用户无感知。
	switch cfg.Env {
	case EnvDevelopment, EnvProduction, EnvTest:
	default:
		return Config{}, fmt.Errorf("READER_ENV must be one of %s, %s, %s (got %q)", EnvDevelopment, EnvProduction, EnvTest, cfg.Env)
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
		"checkConcurrency":            cfg.CheckConcurrency,
		"scriptTimeout":               cfg.ScriptTimeout.String(),
		"debugBodyLimit":              cfg.DebugBodyLimit,
		"contentReSegment":            cfg.ContentReSegment,
		"contentChineseConverterType": cfg.ContentChineseConverterType,
		"searchSourceTimeout":         cfg.SearchSourceTimeout.String(),
		"goMemLimitRatio":             cfg.GoMemLimitRatio,
	}
}

// RequireTokenForAddr 校验监听地址与 token 的组合：token 为空时只允许监听回环地址
// （127.0.0.0/8、::1、localhost），否则拒绝启动。适用于所有环境。
func RequireTokenForAddr(addr, token string) error {
	if token != "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(addr); err == nil && isLoopbackHost(host) {
		return nil
	}
	return fmt.Errorf("READER_TOKEN is required when listening on non-loopback address %q (listen on 127.0.0.1 or set READER_TOKEN)", addr)
}

// isLoopbackHost 判断监听 host 是否仅绑定回环；空 host 与 0.0.0.0/:: 绑定全部网卡，不算回环。
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func stringValue(source Source, key string, fallback string) string {
	if value, ok := source.Lookup(key); ok {
		return value
	}
	return fallback
}

// dataDirSQLiteDSN 解析 SQLite 路径：READER_SQLITE_DSN 显式设置时优先使用，
// 否则默认落在 READER_DATA_DIR 下的 reader.db（默认即 data/reader.db）。
func dataDirSQLiteDSN(source Source, dataDir string) string {
	if value, ok := source.Lookup("READER_SQLITE_DSN"); ok {
		return value
	}
	return filepath.Join(dataDir, "reader.db")
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

func intValue(source Source, key string, fallback int, min int, max int) (int, error) {
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
	if parsed > max {
		return 0, fmt.Errorf("%s must be <= %d", key, max)
	}
	return parsed, nil
}

// floatValue 读取浮点配置项；缺失或空串时返回 fallback，越界（<min 或 >max）时报错。
func floatValue(source Source, key string, fallback float64, min, max float64) (float64, error) {
	value, ok := source.Lookup(key)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", key, err)
	}
	if parsed < min || parsed > max {
		return 0, fmt.Errorf("%s must be between %g and %g", key, min, max)
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
