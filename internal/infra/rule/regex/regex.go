package regex

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/dlclark/regexp2"

	"reader/internal/infra/rule/internal/lru"
)

var ErrInvalidPattern = errors.New("invalid regex pattern")

// patternCacheSize 为正则编译缓存容量；规则中的正则数量有限，按 pattern 字符串复用编译结果。
const patternCacheSize = 512

type Adapter struct {
	maxInputBytes int
}

type compiledPattern struct {
	std *regexp.Regexp
	re2 *regexp2.Regexp
}

// compileResult 同时缓存编译失败结果，避免非法正则在每次调用时重复编译。
type compileResult struct {
	pattern compiledPattern
	err     error
}

var patternCache = lru.New[string, compileResult](patternCacheSize)

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

// Replace 对齐 Legado AnalyzeRule.replaceRegex：
//   - firstOnly（##regex##replacement###）：取第一个匹配片段，并在该片段上做一次替换后返回；无匹配返回空串。
//   - 否则对整段输入做全部替换。
//   - 正则非法时降级为字面量：firstOnly 返回 replacement，否则按字面量全部替换。
func Replace(input string, pattern string, replacement string, firstOnly bool) (string, error) {
	re, err := compilePattern(pattern)
	if err != nil {
		if firstOnly {
			return replacement, nil
		}
		return strings.ReplaceAll(input, pattern, replacement), nil
	}
	return re.Replace(input, replacement, firstOnly)
}

// MatchString 使用缓存的编译结果判断 value 是否包含 pattern 的匹配（Go 标准正则优先，失败回退 regexp2）。
func MatchString(pattern string, value string) (bool, error) {
	re, err := compilePattern(pattern)
	if err != nil {
		return false, err
	}
	if re.std != nil {
		return re.std.MatchString(value), nil
	}
	return re.re2.MatchString(value)
}

func normalizeGroups(groups []string) []string {
	out := make([]string, len(groups))
	copy(out, groups)
	return out
}

func compilePattern(pattern string) (compiledPattern, error) {
	result := patternCache.GetOrCompute(pattern, func() compileResult {
		if re, err := regexp.Compile(pattern); err == nil {
			return compileResult{pattern: compiledPattern{std: re}}
		}
		re, err := regexp2.Compile(pattern, regexp2.None)
		if err != nil {
			return compileResult{err: errors.Join(ErrInvalidPattern, err)}
		}
		re.MatchTimeout = time.Second
		return compileResult{pattern: compiledPattern{re2: re}}
	})
	return result.pattern, result.err
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
		loc := p.std.FindStringIndex(input)
		if loc == nil {
			return "", nil
		}
		return p.replaceFirstStd(input[loc[0]:loc[1]], replacement), nil
	}
	if !firstOnly {
		return p.re2.Replace(input, replacement, 0, -1)
	}
	match, err := p.re2.FindStringMatch(input)
	if err != nil {
		return "", err
	}
	if match == nil {
		return "", nil
	}
	// 与 Legado matcher.group(0).replaceFirst(regex, replacement) 一致：在匹配片段上再做一次首个替换。
	return p.re2.Replace(match.String(), replacement, 0, 1)
}

// replaceFirstStd 在 text 上做一次首个匹配替换（Java String.replaceFirst 语义）。
func (p compiledPattern) replaceFirstStd(text string, replacement string) string {
	loc := p.std.FindStringSubmatchIndex(text)
	if loc == nil {
		return text
	}
	expanded := p.std.ExpandString(nil, replacement, text, loc)
	return text[:loc[0]] + string(expanded) + text[loc[1]:]
}

func regexp2Groups(match *regexp2.Match) []string {
	groups := make([]string, 0, match.GroupCount())
	for i := range match.GroupCount() {
		group := match.GroupByNumber(i)
		if group == nil {
			groups = append(groups, "")
			continue
		}
		groups = append(groups, group.String())
	}
	return groups
}
