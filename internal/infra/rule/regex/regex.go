package regex

import (
	"errors"
	"regexp"
	"time"

	"github.com/dlclark/regexp2"
)

var ErrInvalidPattern = errors.New("invalid regex pattern")

type Adapter struct {
	maxInputBytes int
}

type compiledPattern struct {
	std *regexp.Regexp
	re2 *regexp2.Regexp
}

func New(maxInputBytes int) *Adapter {
	return &Adapter{maxInputBytes: maxInputBytes}
}

func (a *Adapter) Element(input string, patterns ...string) ([]string, error) {
	if a.maxInputBytes > 0 && len(input) > a.maxInputBytes {
		input = input[:a.maxInputBytes]
	}
	if len(patterns) == 0 {
		return nil, nil
	}
	current := input
	for i, pattern := range patterns {
		re, err := compilePattern(pattern)
		if err != nil {
			return nil, err
		}
		matches, err := re.FindAllStringSubmatch(current)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, nil
		}
		if i == len(patterns)-1 {
			return normalizeGroups(matches[0]), nil
		}
		next := ""
		for _, match := range matches {
			next += match[0]
		}
		current = next
	}
	return nil, nil
}

func (a *Adapter) Elements(input string, patterns ...string) ([][]string, error) {
	if a.maxInputBytes > 0 && len(input) > a.maxInputBytes {
		input = input[:a.maxInputBytes]
	}
	if len(patterns) == 0 {
		return nil, nil
	}
	current := input
	for i, pattern := range patterns {
		re, err := compilePattern(pattern)
		if err != nil {
			return nil, err
		}
		matches, err := re.FindAllStringSubmatch(current)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return [][]string{}, nil
		}
		if i == len(patterns)-1 {
			out := make([][]string, 0, len(matches))
			for _, match := range matches {
				out = append(out, normalizeGroups(match))
			}
			return out, nil
		}
		next := ""
		for _, match := range matches {
			next += match[0]
		}
		current = next
	}
	return [][]string{}, nil
}

func Replace(input string, pattern string, replacement string, firstOnly bool) (string, error) {
	re, err := compilePattern(pattern)
	if err != nil {
		return "", err
	}
	return re.Replace(input, replacement, firstOnly)
}

func normalizeGroups(groups []string) []string {
	out := make([]string, len(groups))
	copy(out, groups)
	return out
}

func compilePattern(pattern string) (compiledPattern, error) {
	if re, err := regexp.Compile(pattern); err == nil {
		return compiledPattern{std: re}, nil
	}
	re, err := regexp2.Compile(pattern, regexp2.None)
	if err != nil {
		return compiledPattern{}, errors.Join(ErrInvalidPattern, err)
	}
	re.MatchTimeout = time.Second
	return compiledPattern{re2: re}, nil
}

func (p compiledPattern) FindAllStringSubmatch(input string) ([][]string, error) {
	if p.std != nil {
		return p.std.FindAllStringSubmatch(input, -1), nil
	}
	match, err := p.re2.FindStringMatch(input)
	if err != nil {
		return nil, err
	}
	out := [][]string{}
	for match != nil {
		out = append(out, regexp2Groups(match))
		match, err = p.re2.FindNextMatch(match)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (p compiledPattern) Replace(input string, replacement string, firstOnly bool) (string, error) {
	if p.std != nil {
		if !firstOnly {
			return p.std.ReplaceAllString(input, replacement), nil
		}
		loc := p.std.FindStringSubmatchIndex(input)
		if loc == nil {
			return input, nil
		}
		return string(p.std.ExpandString([]byte{}, replacement, input, loc)), nil
	}
	if !firstOnly {
		return p.re2.Replace(input, replacement, 0, -1)
	}
	match, err := p.re2.FindStringMatch(input)
	if err != nil {
		return "", err
	}
	if match == nil {
		return input, nil
	}
	return p.re2.Replace(match.String(), replacement, 0, 1)
}

func regexp2Groups(match *regexp2.Match) []string {
	groups := make([]string, 0, match.GroupCount())
	for i := 0; i < match.GroupCount(); i++ {
		group := match.GroupByNumber(i)
		if group == nil {
			groups = append(groups, "")
			continue
		}
		groups = append(groups, group.String())
	}
	return groups
}
