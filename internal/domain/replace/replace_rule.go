package replace

import (
	"errors"
	"regexp"
)

const DefaultTimeoutMillisecond int64 = 3000

var (
	ErrEmptyPattern = errors.New("replace pattern is required")
	ErrInvalidRegex = errors.New("replace regex is invalid")
)

type ReplaceRule struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Group              string `json:"group,omitempty"`
	Pattern            string `json:"pattern"`
	Replacement        string `json:"replacement"`
	Scope              string `json:"scope,omitempty"`
	ScopeTitle         bool   `json:"scopeTitle"`
	ScopeContent       bool   `json:"scopeContent"`
	ExcludeScope       string `json:"excludeScope,omitempty"`
	IsEnabled          bool   `json:"isEnabled"`
	IsRegex            bool   `json:"isRegex"`
	TimeoutMillisecond int64  `json:"timeoutMillisecond"`
	Order              int    `json:"order"`
}

func (r ReplaceRule) Validate() error {
	if r.Pattern == "" {
		return ErrEmptyPattern
	}
	if r.IsRegex {
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return errors.Join(ErrInvalidRegex, err)
		}
		if len(r.Pattern) > 0 && r.Pattern[len(r.Pattern)-1] == '|' && (len(r.Pattern) < 2 || r.Pattern[len(r.Pattern)-2] != '\\') {
			return ErrInvalidRegex
		}
	}
	return nil
}

func (r ReplaceRule) ValidTimeoutMillisecond() int64 {
	if r.TimeoutMillisecond <= 0 {
		return DefaultTimeoutMillisecond
	}
	return r.TimeoutMillisecond
}
