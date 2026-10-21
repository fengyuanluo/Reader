package source

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"reader/internal/domain/rule"
)

var (
	ErrMissingBookSourceURL  = errors.New("bookSourceUrl is required")
	ErrMissingBookSourceName = errors.New("bookSourceName is required")
	ErrNotFound              = errors.New("book source not found")
	// ErrConflict 表示书源与已有书源的唯一键（sourceId / bookSourceUrl）冲突。
	ErrConflict = errors.New("book source conflicts with an existing source")
)

type BookSource struct {
	SourceID          string                           `json:"sourceId,omitempty"`
	BookSourceURL     string                           `json:"bookSourceUrl"`
	BookSourceName    string                           `json:"bookSourceName"`
	BookSourceGroup   string                           `json:"bookSourceGroup,omitempty"`
	BookSourceType    int                              `json:"bookSourceType"`
	BookURLPattern    string                           `json:"bookUrlPattern,omitempty"`
	CustomOrder       int                              `json:"customOrder"`
	Enabled           bool                             `json:"enabled"`
	EnabledExplore    bool                             `json:"enabledExplore"`
	EventListener     bool                             `json:"eventListener"`
	CustomButton      bool                             `json:"customButton"`
	JSLib             string                           `json:"jsLib,omitempty"`
	EnabledCookieJar  bool                             `json:"enabledCookieJar"`
	ConcurrentRate    string                           `json:"concurrentRate,omitempty"`
	Header            string                           `json:"header,omitempty"`
	LoginURL          string                           `json:"loginUrl,omitempty"`
	LoginUI           string                           `json:"loginUi,omitempty"`
	LoginCheckJS      string                           `json:"loginCheckJs,omitempty"`
	CoverDecodeJS     string                           `json:"coverDecodeJs,omitempty"`
	BookSourceComment string                           `json:"bookSourceComment,omitempty"`
	VariableComment   string                           `json:"variableComment,omitempty"`
	LastUpdateTime    int64                            `json:"lastUpdateTime"`
	RespondTime       int64                            `json:"respondTime"`
	Weight            int                              `json:"weight"`
	ExploreURL        string                           `json:"exploreUrl,omitempty"`
	ExploreScreen     string                           `json:"exploreScreen,omitempty"`
	RuleExplore       rule.Flexible[rule.ExploreRule]  `json:"ruleExplore,omitempty"`
	SearchURL         string                           `json:"searchUrl,omitempty"`
	RuleSearch        rule.Flexible[rule.SearchRule]   `json:"ruleSearch,omitempty"`
	RuleBookInfo      rule.Flexible[rule.BookInfoRule] `json:"ruleBookInfo,omitempty"`
	RuleToc           rule.Flexible[rule.TocRule]      `json:"ruleToc,omitempty"`
	RuleContent       rule.Flexible[rule.ContentRule]  `json:"ruleContent,omitempty"`
	RuleReview        json.RawMessage                  `json:"ruleReview,omitempty"`
	RawJSON           json.RawMessage                  `json:"-"`
}

// DefaultRespondTime 对齐 Legado BookSource.kt:77 的默认响应时间（毫秒）。
const DefaultRespondTime int64 = 180000

func (s *BookSource) Validate() error {
	if strings.TrimSpace(s.BookSourceURL) == "" {
		return ErrMissingBookSourceURL
	}
	if strings.TrimSpace(s.BookSourceName) == "" {
		return ErrMissingBookSourceName
	}
	return nil
}

func (s *BookSource) Normalize() error {
	if err := s.Validate(); err != nil {
		return err
	}
	if s.SourceID == "" {
		s.SourceID = SourceIDFromURL(s.BookSourceURL)
	}
	// 0 为无效值，按 Legado 默认响应时间补齐；JSON 路径随后由 ApplyLegadoDefaults
	// 按“字段是否存在”覆盖回显式写出的值（含 0）。
	if s.RespondTime <= 0 {
		s.RespondTime = DefaultRespondTime
	}
	return nil
}

// UnmarshalJSON 解析书源 JSON，并为缺省字段套用 Legado 默认值：
// enabled / enabledExplore / enabledCookieJar 默认 true（BookSource.kt:52-57），
// respondTime 默认 180000（BookSource.kt:77）。显式写出的值（含 false）保持不变，
// 显式的 null 按缺省处理。
func (s *BookSource) UnmarshalJSON(data []byte) error {
	type plain BookSource
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*s = BookSource(decoded)
	return s.ApplyLegadoDefaults(data)
}

// ApplyLegadoDefaults 为 JSON 中缺省的字段套用 Legado 默认值。
// HTTP 层从 OpenAPI DTO 逐字段构造书源时同样需要它：只有 JSON 原文才知道字段是否缺省。
func (s *BookSource) ApplyLegadoDefaults(data []byte) error {
	present := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &present); err != nil {
		return err
	}
	applyBool := func(name string, target *bool, fallback bool) {
		raw, ok := present[name]
		if !ok || string(bytes.TrimSpace(raw)) == "null" {
			*target = fallback
			return
		}
		var value bool
		if err := json.Unmarshal(raw, &value); err == nil {
			*target = value
		}
	}
	applyBool("enabled", &s.Enabled, true)
	applyBool("enabledExplore", &s.EnabledExplore, true)
	applyBool("enabledCookieJar", &s.EnabledCookieJar, true)
	if raw, ok := present["respondTime"]; ok && string(bytes.TrimSpace(raw)) != "null" {
		var value int64
		if err := json.Unmarshal(raw, &value); err == nil {
			s.RespondTime = value
		}
	} else {
		s.RespondTime = DefaultRespondTime
	}
	return nil
}

func SourceIDFromURL(bookSourceURL string) string {
	sum := sha256.Sum256([]byte(bookSourceURL))
	return "src_" + hex.EncodeToString(sum[:])[:24]
}

func ParseBookSource(data []byte) (*BookSource, error) {
	var source BookSource
	if err := json.Unmarshal(data, &source); err != nil {
		return nil, err
	}
	source.RawJSON = append(source.RawJSON[:0], data...)
	if err := source.Normalize(); err != nil {
		return nil, err
	}
	// Normalize 会把 respondTime 的零值补成默认值，这里再按 JSON 原文回填显式字段。
	if err := source.ApplyLegadoDefaults(data); err != nil {
		return nil, err
	}
	return &source, nil
}
