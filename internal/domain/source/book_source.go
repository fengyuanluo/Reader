package source

import (
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

type bookSourceJSON BookSource

func (s *BookSource) UnmarshalJSON(data []byte) error {
	decoded := bookSourceJSON{
		Enabled:          true,
		EnabledExplore:   true,
		EnabledCookieJar: true,
		RespondTime:      180000,
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*s = BookSource(decoded)
	return nil
}

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
	return &source, nil
}
