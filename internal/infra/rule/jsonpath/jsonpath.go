package jsonpath

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/PaesslerAG/gval"
	pjsonpath "github.com/PaesslerAG/jsonpath"

	"reader/internal/infra/rule/internal/lru"
	"reader/internal/infra/rule/parser"
	"reader/internal/infra/rule/regex"
)

var (
	ErrInvalidJSON = errors.New("invalid json")
	ErrRuleParse   = errors.New("rule_parse_failed")
)

// regexMatchOperator 实现 jayway JsonPath 的 `=~` 正则过滤运算符（如 [?(@.t=~/a.c/)]）：
// 左侧为字符串，右侧为 `/.../` 包裹的正则字面量，编译为包含匹配。
var regexMatchOperator = gval.InfixOperator("=~", func(a, b any) (any, error) {
	left, ok := a.(string)
	if !ok {
		if a == nil {
			left = ""
		} else {
			left = fmt.Sprint(a)
		}
	}
	pattern, ok := b.(string)
	if !ok {
		if b == nil {
			pattern = ""
		} else {
			pattern = fmt.Sprint(b)
		}
	}
	matched, err := regex.MatchString(strings.TrimSpace(pattern), left)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRuleParse, err)
	}
	return matched, nil
})

// regexFilterPattern 匹配 jayway JsonPath 的 `/.../` 包裹正则字面量（可带尾部标志位）。
var regexFilterPattern = regexp.MustCompile(`(=~)\s*/((?:[^/\\]|\\.)*)/([a-zA-Z]*)`)

// normalizeRegexFilter 把过滤表达式中的 `/.../` 正则字面量改写为 gval 可扫描的
// 双引号字符串：`@.t=~/a.c/` → `@.t=~"a.c"`。pjsonpath.Language() 的扫描器在
// 引号外遇到 `/` 会报 "unexpected /"，因此 `/.../` 写法必须先归一（S07-03）；
// 引号内的 `\"` 转义由 gval 字符串扫描处理，正则内的 `\"` 写成 `\\\"`。
func normalizeRegexFilter(rule string) string {
	if !strings.Contains(rule, "=~") || !regexFilterPattern.MatchString(rule) {
		return rule
	}
	return regexFilterPattern.ReplaceAllStringFunc(rule, func(m string) string {
		sub := regexFilterPattern.FindStringSubmatch(m)
		pattern := strings.ReplaceAll(sub[2], `"`, `\"`)
		return sub[1] + `"` + pattern + `"`
	})
}

// language 在 JSONPath 基础语言上补齐数值/文本比较与逻辑运算，
// 使过滤表达式支持 Legado（jayway）常用的 > < >= <= && || ! 与 =~ 等运算符。
var language = gval.NewLanguage(
	pjsonpath.Language(),
	gval.Arithmetic(),
	gval.Text(),
	gval.PropositionalLogic(),
	regexMatchOperator,
)

// evaluableCacheSize 为 JSONPath 编译缓存容量，按规则字符串复用解析结果。
const evaluableCacheSize = 512

type evaluableResult struct {
	eval gval.Evaluable
	err  error
}

var evaluableCache = lru.New[string, evaluableResult](evaluableCacheSize)

func compilePath(path string) (gval.Evaluable, error) {
	result := evaluableCache.GetOrCompute(path, func() evaluableResult {
		eval, err := language.NewEvaluable(path)
		return evaluableResult{eval: eval, err: err}
	})
	return result.eval, result.err
}

type Adapter struct {
	data any
}

func New(input string) (*Adapter, error) {
	var data any
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	return &Adapter{data: data}, nil
}

// NewFromData 复用调用方已反序列化的 JSON 数据，避免同一步内对同一输入二次 Unmarshal。
// data 仅被读取，不会修改。
func NewFromData(data any) *Adapter {
	return &Adapter{data: data}
}

func (a *Adapter) Object(rule string) (any, error) {
	rule = strings.TrimSpace(strings.TrimPrefix(rule, "@Json:"))
	rule = normalizeRule(rule)
	if rule == "" {
		return nil, fmt.Errorf("%w: empty jsonpath", ErrRuleParse)
	}
	eval, err := compilePath(rule)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRuleParse, err)
	}
	value, err := eval(context.Background(), a.data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRuleParse, err)
	}
	return value, nil
}

func normalizeRule(rule string) string {
	if strings.HasPrefix(rule, "$.[") {
		rule = "$" + strings.TrimPrefix(rule, "$.")
	}
	return normalizeSingleQuotedStrings(normalizeRegexFilter(rule))
}

func normalizeSingleQuotedStrings(rule string) string {
	if !strings.Contains(rule, "'") {
		return rule
	}
	var out strings.Builder
	for i := 0; i < len(rule); {
		if rule[i] != '\'' {
			out.WriteByte(rule[i])
			i++
			continue
		}
		i++
		var value strings.Builder
		closed := false
		for i < len(rule) {
			if rule[i] == '\'' {
				i++
				closed = true
				break
			}
			if rule[i] == '\\' && i+1 < len(rule) {
				next := rule[i+1]
				switch next {
				case '\'', '\\':
					value.WriteByte(next)
				case 'n':
					value.WriteByte('\n')
				case 'r':
					value.WriteByte('\r')
				case 't':
					value.WriteByte('\t')
				default:
					value.WriteByte('\\')
					value.WriteByte(next)
				}
				i += 2
				continue
			}
			value.WriteByte(rule[i])
			i++
		}
		if !closed {
			return rule
		}
		encoded, _ := json.Marshal(value.String())
		out.Write(encoded)
	}
	return out.String()
}

func (a *Adapter) List(rule string) ([]any, error) {
	values, err := a.values(rule)
	if err != nil {
		return nil, err
	}
	return values, nil
}

func (a *Adapter) Strings(rule string) ([]string, error) {
	values, err := a.values(rule)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, stringify(value))
	}
	return out, nil
}

func (a *Adapter) String(rule string) (string, error) {
	values, err := a.Strings(rule)
	if err != nil {
		return "", err
	}
	return strings.Join(values, "\n"), nil
}

func (a *Adapter) values(rule string) ([]any, error) {
	chain, err := parser.Split(strings.TrimSpace(rule), "&&", "||", "%%")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRuleParse, err)
	}
	results := [][]any{}
	for _, step := range chain.Steps {
		value, err := a.Object(step.Raw)
		if err != nil {
			if isEmptyTraversalError(err) || len(chain.Steps) > 1 {
				continue
			}
			return nil, err
		}
		values := flatten(value)
		if len(values) > 0 {
			results = append(results, values)
			if chain.Separator == "||" {
				break
			}
		}
	}
	if chain.Separator == "%%" {
		return interleave(results), nil
	}
	out := []any{}
	for _, values := range results {
		out = append(out, values...)
	}
	return out, nil
}

// isEmptyTraversalError 判断是否为在标量（float64/string/bool/nil 等）上继续取子路径，或键/下标不存在：
// PaesslerAG/jsonpath 以 %T 输出类型名（如 float64），Legado 对这类异常容错返回空。
func isEmptyTraversalError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "unknown key ") || strings.Contains(msg, " out of bounds") {
		return true
	}
	idx := strings.Index(msg, "unsupported value type ")
	return idx >= 0 && strings.Contains(msg[idx:], " for select")
}

func flatten(value any) []any {
	if value == nil {
		return nil
	}
	if values, ok := value.([]any); ok {
		return values
	}
	return []any{value}
}

func stringify(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		raw, err := json.Marshal(v)
		if err == nil {
			if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
				var s string
				if json.Unmarshal(raw, &s) == nil {
					return s
				}
			}
			return string(raw)
		}
		return fmt.Sprint(v)
	}
}

func interleave(results [][]any) []any {
	if len(results) == 0 {
		return nil
	}
	out := []any{}
	for i := range results[0] {
		for _, values := range results {
			if i < len(values) {
				out = append(out, values[i])
			}
		}
	}
	return out
}
