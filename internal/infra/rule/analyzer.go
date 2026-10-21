package ruleinfra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"

	domainbook "reader/internal/domain/book"
	domainrule "reader/internal/domain/rule"
	"reader/internal/infra/rule/css"
	"reader/internal/infra/rule/internal/lru"
	"reader/internal/infra/rule/jsonpath"
	"reader/internal/infra/rule/regex"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/rule/xpath"
)

var (
	ErrScriptUnavailable = errors.New("script runtime is not configured")
)

type Mode string

const (
	ModeDefault Mode = "default"
	ModeXPath   Mode = "xpath"
	ModeJSON    Mode = "json"
	ModeRegex   Mode = "regex"
	ModeJS      Mode = "js"
)

type Context struct {
	BaseURL     string
	RedirectURL string
	SourceURL   string
	Script      domainrule.ScriptRuntime
	MaxRegexIn  int
	Chapter     any
	Book        any
	Source      any
}

type Analyzer struct {
	ctx  Context
	vars map[string]string
	// shared 标记 vars 为调用方传入的共享只读表（ShareVariables）：首次写入前写时复制为私有。
	shared bool
	// baseline 为最近一次 SetVariables/ShareVariables 的基线拷贝，SetChapter 恢复用。
	baseline map[string]string
	// evalDepth 为当前求值递归深度，超上限返回原 token（防恶意规则深递归栈溢出）。
	evalDepth int
}

// maxEvalDepth 限制规则展开的递归深度（expandRawString→GetString→evalStep→makeUpString
// 循环通路）；书源规则来自用户导入属不可信输入。
const maxEvalDepth = 16

func NewAnalyzer(ctx Context) *Analyzer {
	return &Analyzer{ctx: ctx, vars: map[string]string{}}
}

// SetBook 重绑书籍载体与脚本适配器（S14-07：discovery 逐元素复用单个 Analyzer 时更新
// Context.Book；脚本侧 book 绑定来自 ScriptAdapter.Book，必须同步更新，否则 @js: 内
// book.putVariable 等规则在脚本侧读到 nil book）。
func (a *Analyzer) SetBook(book any, script domainrule.ScriptRuntime) {
	a.ctx.Book = book
	if script != nil {
		a.ctx.Script = script
	}
}

// SetChapter 逐章替换章节载体与脚本适配器（S12-06：逐章解析复用同一 Analyzer，仅 Chapter
// 与 Script 变化），并把 vars 重置为基线拷贝（上一章 chapter 级 @put 变量不得泄漏到下一章）。
// 基线在 ShareVariables/SetVariables 时记录。
func (a *Analyzer) SetChapter(chapter any, script domainrule.ScriptRuntime) {
	a.ctx.Chapter = chapter
	a.ctx.Script = script
	a.resetVarsToBaseline()
}

// resetVarsToBaseline 把 vars 恢复为最近一次 SetVariables/ShareVariables 的基线拷贝。
func (a *Analyzer) resetVarsToBaseline() {
	if a.baseline == nil {
		a.vars = map[string]string{}
		a.shared = false
		return
	}
	vars := make(map[string]string, len(a.baseline))
	for key, value := range a.baseline {
		vars[key] = value
	}
	a.vars = vars
	a.shared = false
}

// ShareVariables 与其他 Analyzer 共享同一份只读变量表（写时复制：首次写入前不拷贝），
// 供万章目录逐章解析复用顶层 baseVars，避免每章一次完整 map 深拷贝。
// 并发安全由调用方保证（目录逐章为串行）。
func (a *Analyzer) ShareVariables(vars map[string]string) {
	a.vars = vars
	a.shared = true
	if len(vars) == 0 {
		a.baseline = nil
	} else {
		baseline := make(map[string]string, len(vars))
		for key, value := range vars {
			baseline[key] = value
		}
		a.baseline = baseline
	}
}

// writeVar 写入 analyzer 级变量：共享表写时复制，并把写入同步进基线之外的章节级变量
// （基线内容不覆盖，保证 SetChapter 恢复基线后章节级 @put 不跨章泄漏）。
func (a *Analyzer) writeVar(key string, value string) {
	if a.shared {
		vars := make(map[string]string, len(a.vars)+1)
		for k, v := range a.vars {
			vars[k] = v
		}
		a.vars = vars
		a.shared = false
	}
	a.vars[key] = value
}

// GetString 求值规则并返回合并后的文本；结果含 & 时反转义 HTML 实体（对齐 Legado getString 默认 unescape=true）。
func (a *Analyzer) GetString(ctx context.Context, rule string, input any) (string, error) {
	text, err := a.GetStringRaw(ctx, rule, input)
	if err != nil {
		return "", err
	}
	return unescapeHTMLEntities(text), nil
}

// GetStringRaw 与 GetString 相同但不做 HTML 实体反转义（对应 Legado getString(rule, unescape=false)，用于正文）。
func (a *Analyzer) GetStringRaw(ctx context.Context, rule string, input any) (string, error) {
	if strings.TrimSpace(rule) == "" {
		return "", nil
	}
	if shouldShortCircuitTemplateRule(rule) {
		return a.renderTemplate(ctx, rule, input)
	}
	if strings.Contains(rule, "{$.") && !strings.Contains(rule, "{{") {
		return a.renderJSONPathTemplate(ctx, rule, input)
	}
	values, err := a.evalStringSteps(ctx, rule, input, true)
	if err != nil || len(values) == 0 {
		return "", err
	}
	return strings.Join(values, "\n"), nil
}

// GetURL 求值 URL 类规则（对应 Legado getString(rule, isUrl=true)）：取首个非空结果，
// 反转义实体后按 RedirectURL 绝对化；无结果时回退绝对化 baseUrl（对齐 Legado
// AnalyzeRule.getString 尾部 isUrl 分支：str 为空返回 baseUrl ?: ""）。
func (a *Analyzer) GetURL(ctx context.Context, rule string, input any) (string, error) {
	values, err := a.GetStringList(ctx, rule, input)
	if err != nil {
		return "", err
	}
	for _, value := range values {
		value = strings.TrimSpace(unescapeHTMLEntities(value))
		if value != "" {
			return strings.TrimSpace(urlrule.Absolute(a.ctx.RedirectURL, value)), nil
		}
	}
	// 无结果回退 baseUrl（Legado 语义）；baseUrl 也为空时返回空串。
	if a.ctx.BaseURL != "" {
		return strings.TrimSpace(urlrule.Absolute(a.ctx.RedirectURL, a.ctx.BaseURL)), nil
	}
	return "", nil
}

// GetURLList 求值 URL 列表规则（对应 Legado getStringList(rule, isUrl=true)）：
// 每项按 RedirectURL 绝对化，丢弃空项并去重（Legado AnalyzeRule.kt:231-240 urlList
// 同样 contains 去重），保持首次出现顺序。
func (a *Analyzer) GetURLList(ctx context.Context, rule string, input any) ([]string, error) {
	values, err := a.GetStringList(ctx, rule, input)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		absolute := strings.TrimSpace(urlrule.Absolute(a.ctx.RedirectURL, value))
		if absolute == "" {
			continue
		}
		if _, ok := seen[absolute]; ok {
			continue
		}
		seen[absolute] = struct{}{}
		out = append(out, absolute)
	}
	return out, nil
}

func (a *Analyzer) ExpandString(ctx context.Context, text string, input any) (string, error) {
	return a.expandRawString(ctx, text, input)
}

// GetStringList 求值规则并返回结果列表；与 Legado getStringList 一致，不做实体反转义与 URL 绝对化。
func (a *Analyzer) GetStringList(ctx context.Context, rule string, input any) ([]string, error) {
	return a.getStringList(ctx, rule, input, true)
}

func (a *Analyzer) GetStringListRaw(ctx context.Context, rule string, input any) ([]string, error) {
	return a.getStringList(ctx, rule, input, false)
}

func (a *Analyzer) getStringList(ctx context.Context, rule string, input any, normalizeURLs bool) ([]string, error) {
	if strings.TrimSpace(rule) == "" {
		return nil, nil
	}
	if shouldShortCircuitTemplateRule(rule) {
		value, err := a.renderTemplate(ctx, rule, input)
		if err != nil {
			return nil, err
		}
		if value == "" {
			return nil, nil
		}
		return stringList(value, normalizeURLs), nil
	}
	if strings.Contains(rule, "{$.") && !strings.Contains(rule, "{{") {
		value, err := a.renderJSONPathTemplate(ctx, rule, input)
		if err != nil {
			return nil, err
		}
		if value == "" {
			return nil, nil
		}
		return stringList(value, normalizeURLs), nil
	}
	return a.evalStringSteps(ctx, rule, input, normalizeURLs)
}

func (a *Analyzer) evalStringSteps(ctx context.Context, rule string, input any, normalizeURLs bool) ([]string, error) {
	// 顶层 || 取首个非空、%% 交错合并（对齐 Legado AnalyzeByJSonPath 的 splitRule("&&","||")
	// 语义：非选择器规则下 || 不应被静默当 && 步进）。选择器规则（含 @/$/css 前缀）的
	// ||/%% 语义由 css/jsonpath 适配器处理（对齐 Legado 子选择器层），不在顶层拆分。
	if !strings.ContainsAny(rule, "@$") {
		if chain, sep, ok := splitTopLevelChain(rule); ok {
			switch sep {
			case "||":
				for _, part := range chain {
					if strings.TrimSpace(part) == "" {
						continue
					}
					values, err := a.evalStringSteps(ctx, part, input, normalizeURLs)
					if err != nil {
						return nil, err
					}
					if hasNonEmpty(values) {
						return values, nil
					}
				}
				return nil, nil
			case "%%":
				all := [][]string{}
				for _, part := range chain {
					if strings.TrimSpace(part) == "" {
						continue
					}
					values, err := a.evalStringSteps(ctx, part, input, normalizeURLs)
					if err != nil {
						return nil, err
					}
					if len(values) > 0 {
						all = append(all, values)
					}
				}
				out := []string{}
				for i := 0; ; i++ {
					added := false
					for _, values := range all {
						if i < len(values) {
							out = append(out, values[i])
							added = true
						}
					}
					if !added {
						return out, nil
					}
				}
			}
		}
	}
	steps := splitSourceRules(rule, false)
	var current any = input
	// 链首把 string 输入包成惰性解析载体，链内各步复用同一份解析结果。
	if text, ok := input.(string); ok {
		current = newParsedInput(text)
	} else if raw, ok := input.([]byte); ok {
		current = newParsedInput(string(raw))
	}
	for _, step := range steps {
		value, err := a.evalStep(ctx, step, current, false)
		if err != nil {
			return nil, err
		}
		current = collapseScalarResult(value)
	}
	values := stringList(current, normalizeURLs)
	return values, nil
}

// hasNonEmpty 判断值列表是否含非空项（顶层 || 短路判定）。
func hasNonEmpty(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

// splitTopLevelChain 按顶层 || 或 %% 切分规则串（引号/平衡组内的分隔符不参与）；
// 无顶层分隔符或同时存在 && 时返回 false（保持步进语义）。
func splitTopLevelChain(rule string) ([]string, string, bool) {
	sep := ""
	for _, candidate := range []string{"||", "%%"} {
		if containsTopLevelSeparator(rule, candidate) {
			sep = candidate
			break
		}
	}
	if sep == "" {
		return nil, "", false
	}
	// && 与 ||/%% 混排时保持步进语义（Legado splitRule 同样按首段分隔符处理，
	// Reader 保守处理：含 && 时不做顶层取或）。
	if containsTopLevelSeparator(rule, "&&") {
		return nil, "", false
	}
	parts := []string{}
	depth := 0
	inString := false
	escaped := false
	partStart := 0
	for i := range rule {
		c := rule[i]
		if escaped {
			escaped = false
			continue
		}
		switch {
		case c == '\\' && inString:
			escaped = true
		case c == '"' || c == '\'':
			if c == '"' || !inString {
				inString = !inString
			}
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case depth == 0 && !inString && strings.HasPrefix(rule[i:], sep):
			parts = append(parts, rule[partStart:i])
			partStart = i + len(sep)
			i += len(sep) - 1
		}
	}
	parts = append(parts, rule[partStart:])
	if len(parts) <= 1 {
		return nil, "", false
	}
	return parts, sep, true
}

// containsTopLevelSeparator 判断规则串中是否存在引号/平衡组外的分隔符。
func containsTopLevelSeparator(rule string, sep string) bool {
	depth := 0
	inString := false
	escaped := false
	for i := range rule {
		c := rule[i]
		if escaped {
			escaped = false
			continue
		}
		switch {
		case c == '\\' && inString:
			escaped = true
		case c == '"' || c == '\'':
			if c == '"' || !inString {
				inString = !inString
			}
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case depth == 0 && !inString && strings.HasPrefix(rule[i:], sep):
			return true
		}
	}
	return false
}

// htmlEntityPattern 仅匹配以分号结尾的命名/数字实体，与 Legado StringEscapeUtils.unescapeHtml4 一致，
// 避免把 URL 查询串中的 "&para=" 等无分号片段误解码。
var htmlEntityPattern = regexp.MustCompile(`&(?:#[0-9]+|#[xX][0-9a-fA-F]+|[A-Za-z][A-Za-z0-9]*);`)

func unescapeHTMLEntities(text string) string {
	if !strings.Contains(text, "&") {
		return text
	}
	return htmlEntityPattern.ReplaceAllStringFunc(text, html.UnescapeString)
}

func (a *Analyzer) GetElements(ctx context.Context, rule string, input any) ([]any, error) {
	if strings.TrimSpace(rule) == "" {
		return []any{}, nil
	}
	steps := splitSourceRules(rule, true)
	var current any = input
	// 链首把 string 输入包成惰性解析载体，链内各步复用同一份解析结果。
	if text, ok := input.(string); ok {
		current = newParsedInput(text)
	} else if raw, ok := input.([]byte); ok {
		current = newParsedInput(string(raw))
	}
	for _, step := range steps {
		value, err := a.evalStep(ctx, step, current, true)
		if err != nil {
			return nil, err
		}
		current = value
	}
	if current == nil {
		return []any{}, nil
	}
	if values, ok := current.([]any); ok {
		return values, nil
	}
	return []any{current}, nil
}

func (a *Analyzer) evalStep(ctx context.Context, step sourceRule, input any, wantElements bool) (any, error) {
	if err := a.putStepVars(ctx, step, input); err != nil {
		return nil, err
	}
	var err error
	step, err = a.makeUpStep(ctx, step, input)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(step.rule) == "" && len(step.putMap) > 0 && step.replaceRegex == "" {
		return input, nil
	}
	if strings.TrimSpace(step.rule) == "" && step.replaceRegex != "" {
		return applyReplace(input, step)
	}
	var result any
	if step.literal && step.mode == ModeDefault {
		result = step.rule
		if step.replaceRegex != "" {
			result, err = applyReplace(result, step)
		}
		return result, err
	}
	switch step.mode {
	case ModeJS:
		if a.ctx.Script == nil {
			return nil, ErrScriptUnavailable
		}
		// parsedInput 是链首 string 输入的惰性解析载体，只服务于 CSS/JSON/XPath 适配器；
		// JS 脚本必须看到原始文本（旧行为是链首 string），否则 result 会变成 Go 结构体。
		result, err = a.ctx.Script.Eval(ctx, step.rule, unwrapParsedInput(input))
	case ModeJSON:
		adapter, e := parsedJSONAdapter(input)
		if e != nil {
			return nil, e
		}
		step.rule = normalizeJSONRule(step.rule)
		if wantElements {
			result, err = adapter.List(step.rule)
		} else {
			result, err = adapter.Strings(step.rule)
		}
	case ModeXPath:
		adapter, e := parsedXPathAdapter(input)
		if e != nil {
			return nil, e
		}
		if wantElements {
			nodes, e := adapter.Nodes(step.rule)
			if e != nil {
				return nil, e
			}
			result = nodesToAny(nodes)
		} else {
			result, err = adapter.Strings(step.rule)
		}
	case ModeRegex:
		adapter := regex.New(a.ctx.MaxRegexIn)
		patterns := splitRegexChain(step.rule)
		// []any 输入先逐项扁平化为 []string（join 前逐项 asString），避免整体 json.Marshal
		// （S06-08：大 jsonpath 列表逐元素序列化开销）。
		regexInput := asString(input)
		if items, ok := input.([]any); ok {
			strs := make([]string, 0, len(items))
			for _, item := range items {
				strs = append(strs, asString(item))
			}
			regexInput = strings.Join(strs, "\n")
		}
		if wantElements {
			groups, e := adapter.Elements(regexInput, patterns...)
			if e != nil {
				return nil, e
			}
			result = groupsToAny(groups)
		} else {
			groups, e := adapter.Element(regexInput, patterns...)
			if e != nil {
				return nil, e
			}
			result = groups
		}
	default:
		if isJSONInput(input) {
			adapter, e := parsedJSONAdapter(input)
			if e != nil {
				return nil, e
			}
			jsonRule := normalizeJSONRule(step.rule)
			if wantElements {
				result, err = adapter.List(jsonRule)
			} else {
				result, err = adapter.Strings(jsonRule)
			}
			if err == nil {
				break
			}
			// 显式 JSONPath（$ 开头或 @Json: 前缀）的解析/取值错误直接上报；
			// 形如 ".name@href" 的 CSS 规则在 JSON 输入下才被规范化为 JSONPath，
			// 解析失败时仍回落到 CSS 适配器（与 Legado 对非 $ 规则的容错一致）。
			if isExplicitJSONPathRule(step.rule) {
				return nil, err
			}
		}
		adapter, e := newCSSAdapter(input)
		if e != nil {
			return nil, e
		}
		if wantElements {
			selected, e := adapter.Elements(step.rule)
			if e != nil {
				return nil, e
			}
			result = selectionToAny(selected)
		} else {
			result, err = adapter.Strings(step.rule)
		}
	}
	if err != nil {
		return nil, err
	}
	if step.replaceRegex != "" {
		result, err = applyReplace(result, step)
	}
	if err != nil {
		return nil, err
	}
	if text, ok := result.(string); ok {
		// makeUpStep 已在完整 raw 上展开时跳过重复 expandRawString（S06-07），
		// 但 JS/脚本步骤的返回值不是来自 step.raw 展开，仍需展开（结果中可能含 @get/{{}}）。
		if step.expanded && step.mode != ModeJS {
			return text, nil
		}
		text = a.makeUpString(ctx, text, input)
		return text, nil
	}
	return result, nil
}

func (a *Analyzer) makeUpString(ctx context.Context, text string, input any) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	expanded, err := a.expandRawString(ctx, text, input)
	if err == nil {
		return expanded
	}
	return text
}

func (a *Analyzer) expandRawString(ctx context.Context, text string, input any) (string, error) {
	if strings.TrimSpace(text) == "" {
		return text, nil
	}
	// 递归深度保护：expandRawString→GetString→evalStep→makeUpString 可自引用循环，
	// 恶意/畸形规则（如自引用 @put 或深层 {{@get:...}}）可造成深递归，超限保留原文本。
	if a.evalDepth >= maxEvalDepth {
		return text, nil
	}
	a.evalDepth++
	defer func() { a.evalDepth-- }()
	text = getRulePattern.ReplaceAllStringFunc(text, func(token string) string {
		// ReplaceAllStringFunc 传入的 token 就是完整匹配，直接用位置切分避免二次 FindStringSubmatch。
		loc := getRulePattern.FindStringIndex(token)
		if loc == nil || loc[0] != 0 || loc[1] != len(token) {
			return token
		}
		key := strings.TrimSpace(strings.TrimPrefix(token[:loc[1]], "@get:{"))
		key = strings.TrimSuffix(key, "}")
		if value, ok := lookupInputField(input, key); ok {
			return value
		}
		return a.lookupVariable(input, key)
	})
	text = jsRulePattern.ReplaceAllStringFunc(text, func(token string) string {
		// ReplaceAllStringFunc 传入的 token 就是完整匹配，直接用位置切分避免二次 FindStringSubmatch。
		loc := jsRulePattern.FindStringIndex(token)
		if loc == nil || loc[0] != 0 || loc[1] != len(token) {
			return token
		}
		expr := strings.TrimSpace(token[2 : len(token)-2])
		if isRuleExpression(expr) {
			value, err := a.GetString(ctx, expr, input)
			if err != nil {
				return token
			}
			return value
		}
		if a.ctx.Script == nil {
			return token
		}
		value, err := a.ctx.Script.Eval(ctx, expr, input)
		if err != nil {
			return token
		}
		return scriptTemplateString(value)
	})
	return text, nil
}

func (a *Analyzer) renderTemplate(ctx context.Context, rule string, input any) (string, error) {
	var b strings.Builder
	rest := rule
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			b.WriteString(rest)
			return b.String(), nil
		}
		b.WriteString(rest[:start])
		rest = rest[start+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			b.WriteString("{{")
			b.WriteString(rest)
			return b.String(), nil
		}
		expr := strings.TrimSpace(rest[:end])
		value, err := a.evalTemplateExpr(ctx, expr, input)
		if err != nil {
			return "", err
		}
		b.WriteString(value)
		rest = rest[end+2:]
	}
}

func (a *Analyzer) renderJSONPathTemplate(ctx context.Context, rule string, input any) (string, error) {
	var err error
	out := jsonTplPattern.ReplaceAllStringFunc(rule, func(token string) string {
		if err != nil {
			return ""
		}
		// ReplaceAllStringFunc 传入的 token 就是完整匹配，直接用位置切分避免二次 FindStringSubmatch。
		loc := jsonTplPattern.FindStringIndex(token)
		if loc == nil || loc[0] != 0 || loc[1] != len(token) {
			return token
		}
		var value string
		value, err = a.GetString(ctx, "$"+token[2:len(token)-1], input)
		return value
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

func (a *Analyzer) evalTemplateExpr(ctx context.Context, expr string, input any) (string, error) {
	switch expr {
	case "source.bookSourceUrl", "source.key":
		if a.ctx.SourceURL != "" {
			return a.ctx.SourceURL, nil
		}
		return a.ctx.BaseURL, nil
	}
	if strings.HasPrefix(expr, "$.") || strings.HasPrefix(expr, "$[") || strings.HasPrefix(expr, "$..") {
		copy := *a
		return copy.GetString(ctx, expr, input)
	}
	if key, ok := parseJavaGetExpr(expr); ok {
		return a.lookupVariable(input, key), nil
	}
	if rule, ok := parseJavaGetStringExpr(expr); ok {
		return a.GetString(ctx, rule, input)
	}
	if arg, ok := parseJavaTimeFormatExpr(expr); ok {
		multiplier := int64(1)
		if before, ok := strings.CutSuffix(strings.TrimSpace(arg), "*1000"); ok {
			arg = strings.TrimSpace(before)
			multiplier = 1000
		}
		value, err := a.evalTemplateExpr(ctx, arg, input)
		if err != nil {
			return "", err
		}
		ms, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		ms *= multiplier
		if ms <= 0 {
			return "", nil
		}
		return time.UnixMilli(ms).Format("2006-01-02 15:04:05"), nil
	}
	if a.ctx.Script != nil {
		value, err := a.ctx.Script.Eval(ctx, expr, input)
		if err != nil {
			return "", err
		}
		return scriptTemplateString(value), nil
	}
	return expr, nil
}

func newCSSAdapter(input any) (*css.Adapter, error) {
	if selection, ok := input.(*goquery.Selection); ok {
		return css.NewFromSelection(selection), nil
	}
	if raw, ok := input.(map[string]any); ok {
		if selection, ok := raw["__selection"].(*goquery.Selection); ok {
			return css.NewFromSelection(selection), nil
		}
	}
	if parsed, ok := input.(*parsedInput); ok {
		return parsed.cssAdapter()
	}
	return css.New(asString(input))
}

// parsedInput 是链首 string 输入的惰性解析载体：同一次规则链内 JSON/CSS/XPath 解析
// 各最多执行一次，后续步骤复用结果，避免同一份 HTML/JSON 被反复解析。
// 注意：该结构在同一条规则链内被多 goroutine 共享的前提不存在（链内串行求值），无需加锁。
type parsedInput struct {
	raw       string
	json      any
	jsonOK    bool
	jsonErr   error
	css       *css.Adapter
	cssErr    error
	cssDone   bool
	xpath     *xpath.Adapter
	xpathErr  error
	xpathDone bool
}

func newParsedInput(raw string) *parsedInput {
	return &parsedInput{raw: raw}
}

// unwrapParsedInput 把惰性解析载体还原成链首的原始文本，供 JS 脚本等只认字符串的消费者使用。
func unwrapParsedInput(input any) any {
	if parsed, ok := input.(*parsedInput); ok {
		return parsed.raw
	}
	return input
}

func (p *parsedInput) jsonAdapter() (*jsonpath.Adapter, error) {
	if !p.jsonOK {
		var data any
		p.jsonErr = json.Unmarshal([]byte(p.raw), &data)
		p.json = data
		p.jsonOK = true
	}
	if p.jsonErr != nil {
		return nil, p.jsonErr
	}
	return jsonpath.NewFromData(p.json), nil
}

func (p *parsedInput) cssAdapter() (*css.Adapter, error) {
	if !p.cssDone {
		p.css, p.cssErr = css.New(p.raw)
		p.cssDone = true
	}
	return p.css, p.cssErr
}

func (p *parsedInput) xpathAdapter() (*xpath.Adapter, error) {
	if !p.xpathDone {
		p.xpath, p.xpathErr = xpath.New(p.raw)
		p.xpathDone = true
	}
	return p.xpath, p.xpathErr
}

// isJSONInputText 判断原始文本是否形如 JSON 对象/数组。
func isJSONInputText(text string) bool {
	text = strings.TrimSpace(text)
	return (strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}")) ||
		(strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]"))
}

// parsedJSONAdapter 求值 JSON 规则：string/parsedInput 输入惰性解析一次后复用；
// 已是结构化 JSON 的输入直接构造适配器，不做二次反序列化。
func parsedJSONAdapter(input any) (*jsonpath.Adapter, error) {
	switch value := input.(type) {
	case *parsedInput:
		return value.jsonAdapter()
	case map[string]any, []any:
		return jsonpath.NewFromData(value), nil
	}
	return jsonpath.New(asString(input))
}

// parsedXPathAdapter 求值 XPath 规则：string/parsedInput 输入惰性解析一次后复用。
func parsedXPathAdapter(input any) (*xpath.Adapter, error) {
	if parsed, ok := input.(*parsedInput); ok {
		return parsed.xpathAdapter()
	}
	return xpath.New(asString(input))
}

func containsScriptRule(rule string) bool {
	lower := strings.ToLower(rule)
	return strings.Contains(lower, "<js>") || strings.Contains(lower, "@js:")
}

func (a *Analyzer) LoadVariables(raw string) {
	for key, value := range decodeVariables(raw) {
		a.vars[key] = value
	}
}

func (a *Analyzer) Variables() map[string]string {
	out := make(map[string]string, len(a.vars))
	for key, value := range a.vars {
		out[key] = value
	}
	return out
}

func (a *Analyzer) SetVariables(vars map[string]string) {
	a.vars = make(map[string]string, len(vars))
	for key, value := range vars {
		a.vars[key] = value
	}
	a.shared = false
	if len(vars) == 0 {
		a.baseline = nil
		return
	}
	baseline := make(map[string]string, len(vars))
	for key, value := range vars {
		baseline[key] = value
	}
	a.baseline = baseline
}

func (a *Analyzer) VariablesJSON() string {
	if len(a.vars) == 0 {
		return ""
	}
	raw, err := json.Marshal(a.vars)
	if err != nil {
		return ""
	}
	return string(raw)
}

func decodeVariables(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &out); err == nil {
		return out
	}
	loose := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &loose); err == nil {
		for key, value := range loose {
			out[key] = asString(value)
		}
		return out
	}
	return nil
}

func shouldShortCircuitTemplateRule(rule string) bool {
	return strings.Contains(rule, "{{") &&
		!strings.Contains(strings.ToLower(rule), "@get:{") &&
		!containsScriptRule(rule) &&
		!strings.Contains(rule, "##")
}

type sourceRule struct {
	rule         string
	mode         Mode
	replaceRegex string
	replacement  string
	replaceFirst bool
	putMap       []putEntry
	// raw 为去掉 @put 后、按 ## 拆分前的完整规则；含 @get/{{}} 时在求值前整体展开再拆分（对齐 Legado makeUpRule）。
	raw     string
	hasEval bool
	// literal 表示主规则（首个 ## 之前）含 @get/{{}}，按 Legado 视为文本模板而非选择器。
	literal bool
	// expanded 标记 makeUpStep 已在完整 raw 上展开，makeUpString 跳过重复展开。
	expanded bool
}

var (
	// putPrefix 为 @put: 前缀的字节形态（大小写不敏感比对用），配合 scanPutBlocks 的平衡括号提取
	// 取代旧的正则 `@put:(\{[^}]+?\})`：旧正则会在第一个 `}` 处截断，嵌套对象/含 `}` 的值静默丢失。
	putPrefix       = []byte{'@', 'p', 'u', 't', ':'}
	getRulePattern  = regexp.MustCompile(`(?i)@get:\{([^}]+?)\}`)
	jsRulePattern   = regexp.MustCompile(`(?is)\{\{([\w\W]*?)\}\}`)
	evalRulePattern = regexp.MustCompile(`(?is)@get:\{[^}]+?\}|\{\{[\w\W]*?\}\}`)
	jsonTplPattern  = regexp.MustCompile(`(?s)\{\$(\.[^{}]+?)\}`)
)

// sourceRuleCache 缓存同一条规则串的分词结果：目录/正文逐元素求值时同一条规则串
// 会被反复分词，分词结果是纯函数且只读共享（evalStep 内所有修改都作用于值拷贝）。
const sourceRuleCacheSize = 1024

var sourceRuleCache = lru.New[sourceRuleCacheKey, []sourceRule](sourceRuleCacheSize)

type sourceRuleCacheKey struct {
	rule     string
	allInOne bool
}

func splitSourceRules(rule string, allInOne bool) []sourceRule {
	return sourceRuleCache.GetOrCompute(sourceRuleCacheKey{rule: rule, allInOne: allInOne}, func() []sourceRule {
		return parseSourceRules(rule, allInOne)
	})
}

// parseSourceRules 是 splitSourceRules 的无缓存实现（对齐原逻辑）。
func parseSourceRules(rule string, allInOne bool) []sourceRule {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return nil
	}
	mode := ModeDefault
	if allInOne && strings.HasPrefix(rule, ":") {
		mode = ModeRegex
		rule = strings.TrimPrefix(rule, ":")
	}
	return splitEmbeddedJSRules(rule, mode)
}

// hasPrefixIgnoreCase 大小写不敏感地判断 s 是否以 prefix 开头（ASCII，无分配）。
func hasPrefixIgnoreCase(s string, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := range prefix {
		a := s[i]
		b := prefix[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}

// hasSuffixIgnoreCase 大小写不敏感地判断 s 是否以 suffix 结尾（ASCII，无分配）。
func hasSuffixIgnoreCase(s string, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	offset := len(s) - len(suffix)
	for i := range suffix {
		a := s[offset+i]
		b := suffix[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}

func parseSourceRule(raw string, mode Mode) sourceRule {
	rule := strings.TrimSpace(raw)
	// S06-14：用前缀字节比较（'@'/'j'/'p'…大小写判断）替代 6+ 次 strings.ToLower 整串分配；
	// 分词只在缓存未命中时执行，数百书源首次搜索时整串 ToLower 是热点小分配。
	switch {
	case mode == ModeRegex:
	case hasPrefixIgnoreCase(rule, "@js:"):
		mode = ModeJS
		rule = strings.TrimSpace(rule[4:])
	case hasPrefixIgnoreCase(rule, "<js>") && hasSuffixIgnoreCase(rule, "</js>"):
		mode = ModeJS
		rule = strings.TrimSpace(rule[4 : len(rule)-5])
	case hasPrefixIgnoreCase(rule, "@xpath:"):
		mode = ModeXPath
		rule = strings.TrimSpace(rule[7:])
	case hasPrefixIgnoreCase(rule, "@css:"):
		rule = strings.TrimSpace(rule[5:])
	case hasPrefixIgnoreCase(rule, "@json:"):
		mode = ModeJSON
		rule = strings.TrimSpace(rule[6:])
	case strings.HasPrefix(rule, "$."):
		mode = ModeJSON
	case strings.HasPrefix(rule, "$["):
		mode = ModeJSON
	case strings.HasPrefix(rule, "/"):
		mode = ModeXPath
	case strings.HasPrefix(rule, "@@"):
		rule = strings.TrimPrefix(rule, "@@")
	}
	// 按平衡括号提取并移除全部 @put:{...} 段（含 `}` 的值不截断），按出现顺序保留键值对。
	putMap := parsePutRules(rule)
	rule = removePutBlocks(rule)
	step := sourceRule{mode: mode, putMap: putMap, raw: rule}
	if loc := evalRulePattern.FindStringIndex(rule); loc != nil {
		step.hasEval = true
		// 对齐 AnalyzeRule.SourceRule init：首个 @get/{{}} 位于 ## 之前时整条规则按模板处理。
		step.literal = loc[0] == 0 || !strings.Contains(rule[:loc[0]], "##")
	}
	step.splitReplace(rule)
	return step
}

// splitReplace 按 ## 拆分出主规则、替换正则、替换文本与 replaceFirst 标记（对齐 makeUpRule 尾部逻辑）。
func (s *sourceRule) splitReplace(rule string) {
	parts := strings.Split(rule, "##")
	s.rule = strings.TrimSpace(parts[0])
	s.replaceRegex, s.replacement, s.replaceFirst = "", "", false
	if len(parts) > 1 {
		s.replaceRegex = parts[1]
	}
	if len(parts) > 2 {
		s.replacement = parts[2]
	}
	if len(parts) > 3 {
		s.replaceFirst = true
	}
}

func splitEmbeddedJSRules(rule string, mode Mode) []sourceRule {
	out := []sourceRule{}
	start := 0
	lower := strings.ToLower(rule)
	for {
		jsStart := strings.Index(lower[start:], "<js>")
		atJSStart := strings.Index(lower[start:], "@js:")
		if jsStart < 0 && atJSStart < 0 {
			break
		}
		useAtJS := false
		relStart := jsStart
		if relStart < 0 || (atJSStart >= 0 && atJSStart < relStart) {
			relStart = atJSStart
			useAtJS = true
		}
		absStart := start + relStart
		if absStart > start {
			if raw := strings.TrimSpace(rule[start:absStart]); raw != "" {
				out = append(out, parseSourceRule(raw, mode))
			}
		}
		if useAtJS {
			scriptText := strings.TrimSpace(rule[absStart+4:])
			if scriptText != "" {
				out = append(out, parseSourceRule("@js:"+scriptText, mode))
			}
			return out
		}
		bodyStart := absStart + len("<js>")
		endRel := strings.Index(lower[bodyStart:], "</js>")
		if endRel < 0 {
			if raw := strings.TrimSpace(rule[absStart:]); raw != "" {
				out = append(out, parseSourceRule(raw, mode))
			}
			return out
		}
		bodyEnd := bodyStart + endRel
		scriptText := strings.TrimSpace(rule[bodyStart:bodyEnd])
		if scriptText != "" {
			out = append(out, parseSourceRule("<js>"+scriptText+"</js>", mode))
		}
		start = bodyEnd + len("</js>")
	}
	if raw := strings.TrimSpace(rule[start:]); raw != "" {
		out = append(out, parseSourceRule(raw, mode))
	}
	return out
}

func splitRegexChain(rule string) []string {
	parts := strings.Split(rule, "&&")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// putEntry 是一条 @put 键值对，保持规则书写顺序（对齐 Legado putMap 的书写语义，
// 使键间依赖 b 依赖 a 时求值结果确定）。
type putEntry struct {
	Key   string
	Value string
}

// parsePutRules 提取规则中全部 @put:{...} 段并按出现顺序返回键值对。
// 对齐 Legado 的平衡组语义（RuleAnalyzer.chompCodeBalanced/splitPutRule）：
// 用括号平衡提取完整 {...}（忽略引号内括号与转义），支持含 `}` 的嵌套值；
// 同名后键覆盖前键（对齐 GSON 后键覆盖语义）。
func parsePutRules(rule string) []putEntry {
	blocks := scanPutBlocks(rule)
	if len(blocks) == 0 {
		return nil
	}
	out := make([]putEntry, 0, len(blocks))
	for _, block := range blocks {
		for _, entry := range parseRuleObjectOrdered(block) {
			replaced := false
			for i := range out {
				if out[i].Key == entry.Key {
					out[i].Value = entry.Value
					replaced = true
					break
				}
			}
			if !replaced {
				out = append(out, entry)
			}
		}
	}
	return out
}

// scanPutBlocks 扫描规则串中每个 @put: 后的完整平衡 {...} 段（引号内括号与 `\` 转义不参与平衡），
// 返回各段花括号内内容；未闭合时返回已找到的段（容错对齐 Legado）。
func scanPutBlocks(rule string) []string {
	blocks := []string{}
	for i := 0; i < len(rule); {
		if !hasPutPrefix(rule[i:]) {
			i++
			continue
		}
		start := i + len(putPrefix)
		for start < len(rule) && (rule[start] == ' ' || rule[start] == '\t') {
			start++
		}
		if start >= len(rule) || rule[start] != '{' {
			i = start
			continue
		}
		content, next := consumeBalancedBraces(rule, start+1)
		blocks = append(blocks, content)
		i = next
	}
	return blocks
}

// hasPutPrefix 大小写不敏感地判断 s 是否以 @put: 开头。
func hasPutPrefix(s string) bool {
	if len(s) < len(putPrefix) {
		return false
	}
	return (s[0] == '@') &&
		(s[1] == 'p' || s[1] == 'P') &&
		(s[2] == 'u' || s[2] == 'U') &&
		(s[3] == 't' || s[3] == 'T') &&
		s[4] == ':'
}

// consumeBalancedBraces 从 pos（首个 `{` 之后）提取平衡 {...} 段的内容与结束位置；
// 引号内的括号与 `\` 转义字符不参与平衡（对齐 parser.consumeBalanced/chompRuleBalanced）。
func consumeBalancedBraces(rule string, pos int) (string, int) {
	start := pos
	depth := 1
	for pos < len(rule) {
		r, width := utf8.DecodeRuneInString(rule[pos:])
		if r == utf8.RuneError && width == 0 {
			width = 1
		}
		pos += width
		switch r {
		case '\\':
			if pos < len(rule) {
				_, w := utf8.DecodeRuneInString(rule[pos:])
				if w == 0 {
					w = 1
				}
				pos += w
			}
		case '\'', '"':
			// 引号内括号不参与平衡；未闭合引号按 Legado 容错继续扫描。
			for pos < len(rule) {
				q, qw := utf8.DecodeRuneInString(rule[pos:])
				if q == utf8.RuneError && qw == 0 {
					qw = 1
				}
				pos += qw
				if q == r {
					break
				}
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rule[start : pos-1], pos
			}
		}
	}
	return rule[start:], pos
}

// parseRuleObjectOrdered 按出现顺序解析 put 值对象的键值对：优先走严格 JSON（json.Unmarshal
// 不保证键顺序，故严格路径也按键出现顺序解析），失败后按宽松 k:v 解析。
func parseRuleObjectOrdered(raw string) []putEntry {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	strict := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &strict); err == nil {
		out := make([]putEntry, 0, len(strict))
		for _, key := range objectKeyOrder(raw) {
			if value, ok := strict[key]; ok {
				out = append(out, putEntry{Key: key, Value: value})
			}
		}
		return out
	}
	// 宽松路径：按平衡括号切分 k:v（嵌套对象/引号内冒号不切分）。仅当 raw 以 { 开头时剥外层
	// 花括号（平衡提取，不用 TrimSuffix 以免截断值内 `}`）；键前缀形态（key:{...}）不剥。
	if strings.HasPrefix(raw, "{") {
		if content, ok := balancedOuterBraces(raw); ok {
			raw = content
		}
	}
	out := []putEntry{}
	for _, part := range splitLooseObjectParts(raw) {
		key, value, ok := cutLooseKeyValue(part)
		if !ok {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		value = strings.TrimSpace(value)
		// 宽松 JSON 值两侧成对引号剥去（对齐 GSON 引号字符串语义），值内引号保留。
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if key != "" {
			out = append(out, putEntry{Key: key, Value: value})
		}
	}
	return out
}

// balancedOuterBraces 剥去 raw 的最外层平衡花括号（raw 以 { 开头时）；非平衡返回 false。
func balancedOuterBraces(raw string) (string, bool) {
	content, next := consumeBalancedBraces(raw, 1)
	if next != len(raw) {
		return "", false
	}
	return content, true
}

// cutLooseKeyValue 切分宽松对象的单个 k:v 片段：首个不在引号/花括号内的冒号为分隔，
// 值原样保留（含嵌套花括号与引号），仅去除首尾空白；两侧成对引号剥去（值内 `}` 不截断）。
func cutLooseKeyValue(part string) (string, string, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := range part {
		c := part[i]
		if escaped {
			escaped = false
			continue
		}
		switch {
		case c == '\\' && inString:
			escaped = true
		case c == '"' || c == '\'':
			if c == '"' || !inString {
				inString = !inString
			}
		case c == '{':
			depth++
		case c == '}':
			depth--
		case c == ':' && depth == 0 && !inString:
			return part[:i], part[i+1:], true
		}
	}
	return "", "", false
}

// objectKeyOrder 从严格 JSON 对象文本中按出现顺序提取顶层键名（引号内冒号/逗号不切分）。
func objectKeyOrder(raw string) []string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "{") || !strings.HasSuffix(raw, "}") {
		return nil
	}
	raw = raw[1 : len(raw)-1]
	keys := []string{}
	inString := false
	escaped := false
	partStart := 0
	for i := range raw {
		c := raw[i]
		if escaped {
			escaped = false
			continue
		}
		switch {
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case c == ',' && !inString:
			key := decodeJSONKey(raw[partStart:i])
			if key != "" {
				keys = append(keys, key)
			}
			partStart = i + 1
		}
	}
	if key := decodeJSONKey(raw[partStart:]); key != "" {
		keys = append(keys, key)
	}
	return keys
}

// decodeJSONKey 从 "key": 前缀片段解析键名。
func decodeJSONKey(part string) string {
	part = strings.TrimSpace(part)
	key, _, ok := strings.Cut(part, ":")
	if !ok {
		key = part
	}
	key = strings.TrimSpace(key)
	var decoded string
	if err := json.Unmarshal([]byte(key), &decoded); err != nil {
		return strings.Trim(key, `"'`)
	}
	return decoded
}

// splitLooseObjectParts 按逗号切分宽松对象文本，引号与嵌套花括号内的逗号不切分。
func splitLooseObjectParts(raw string) []string {
	parts := []string{}
	depth := 0
	inString := false
	escaped := false
	partStart := 0
	for i := range raw {
		c := raw[i]
		if escaped {
			escaped = false
			continue
		}
		switch {
		case c == '\\' && inString:
			escaped = true
		case c == '"' || c == '\'':
			if c == '"' || !inString {
				inString = !inString
			}
		case c == '{':
			depth++
		case c == '}':
			depth--
		case c == ',' && depth == 0 && !inString:
			parts = append(parts, raw[partStart:i])
			partStart = i + 1
		}
	}
	parts = append(parts, raw[partStart:])
	return parts
}

// removePutBlocks 从规则串中移除全部 @put:{...} 段（平衡括号提取，含 `}` 的值不截断），
// 返回剩余规则文本。
func removePutBlocks(rule string) string {
	for {
		start, end := findPutBlock(rule)
		if end == 0 {
			return rule
		}
		rule = rule[:start] + rule[end:]
	}
}

// findPutBlock 定位首个 @put:{...} 段的起止位置；未找到时 end 返回 0。
func findPutBlock(rule string) (start, end int) {
	for i := range rule {
		if !hasPutPrefix(rule[i:]) {
			continue
		}
		bodyStart := i + len(putPrefix)
		for bodyStart < len(rule) && (rule[bodyStart] == ' ' || rule[bodyStart] == '\t') {
			bodyStart++
		}
		if bodyStart >= len(rule) || rule[bodyStart] != '{' {
			continue
		}
		_, next := consumeBalancedBraces(rule, bodyStart+1)
		return i, next
	}
	return 0, 0
}

func isJSONInput(input any) bool {
	if parsed, ok := input.(*parsedInput); ok {
		return isJSONInputText(parsed.raw)
	}
	text := strings.TrimSpace(asString(input))
	return isJSONInputText(text)
}

// isExplicitJSONPathRule 判断规则是否显式声明为 JSONPath（$ 开头或 @Json: 前缀）；
// 这类规则的解析/取值错误应当上报，而不是回落到 CSS 适配器。
func isExplicitJSONPathRule(rule string) bool {
	trimmed := strings.TrimSpace(rule)
	if strings.HasPrefix(trimmed, "@Json:") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "@Json:"))
	}
	return strings.HasPrefix(trimmed, "$")
}

func normalizeJSONRule(rule string) string {
	rule = strings.TrimSpace(strings.TrimPrefix(rule, "@Json:"))
	if rule == "" || strings.HasPrefix(rule, "$") || strings.HasPrefix(rule, "@") {
		if strings.HasPrefix(rule, "$.[") {
			return "$" + strings.TrimPrefix(rule, "$.")
		}
		return rule
	}
	if strings.HasPrefix(rule, ".[") {
		return "$" + strings.TrimPrefix(rule, ".")
	}
	if strings.HasPrefix(rule, ".") {
		return "$" + rule
	}
	return "$." + rule
}

func parseJavaGetExpr(expr string) (string, bool) {
	key, ok := parseSingleQuotedCall(expr, "java.get")
	return key, ok
}

func parseJavaGetStringExpr(expr string) (string, bool) {
	key, ok := parseSingleQuotedCall(expr, "java.getString")
	return key, ok
}

func parseJavaTimeFormatExpr(expr string) (string, bool) {
	expr = strings.TrimSpace(expr)
	if !strings.HasPrefix(expr, "java.timeFormat(") || !strings.HasSuffix(expr, ")") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(expr, "java.timeFormat("), ")")), true
}

func parseSingleQuotedCall(expr string, name string) (string, bool) {
	expr = strings.TrimSpace(expr)
	prefix := name + "("
	if !strings.HasPrefix(expr, prefix) || !strings.HasSuffix(expr, ")") {
		return "", false
	}
	arg := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(expr, prefix), ")"))
	if len(arg) < 2 {
		return "", false
	}
	if (arg[0] == '\'' && arg[len(arg)-1] == '\'') || (arg[0] == '"' && arg[len(arg)-1] == '"') {
		return arg[1 : len(arg)-1], true
	}
	return "", false
}

func (a *Analyzer) putStepVars(ctx context.Context, step sourceRule, input any) error {
	for _, entry := range step.putMap {
		value, err := a.evalPutRule(ctx, entry.Value, input)
		if err != nil {
			return err
		}
		a.storeVariable(input, entry.Key, value)
		a.writeVar(entry.Key, value)
	}
	return nil
}

func (a *Analyzer) evalPutRule(ctx context.Context, rule string, input any) (string, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return "", nil
	}
	// 对齐 Legado putRule 的 getString 求值语义，同时保持字面量兼容：
	// 仅当 put 值为规则形态（@get/@js/{{/$ 选择器前缀）时走 GetString 求值；
	// 纯字面量（如 {k:1}）直接返回原值（Legado getString 对该类文本同样无法命中选择器，
	// Reader 按字面量保留以支持 @put 字面量与字段引用）。
	if !isRuleShapedValue(rule) {
		if value, ok := lookupInputField(input, rule); ok {
			return value, nil
		}
		return rule, nil
	}
	value, err := a.GetString(ctx, rule, input)
	if err != nil {
		return "", err
	}
	return value, nil
}

// isRuleShapedValue 判断 put 值是否为规则形态：@前缀、$ 选择器前缀、{{ 模板，
// 或两侧成对引号包裹的选择器（如 "[property$=book_name]@content"，Legado put 值经
// getString 求值，宽松 JSON 引号剥去后即选择器）。
func isRuleShapedValue(rule string) bool {
	if strings.HasPrefix(rule, "@") || strings.HasPrefix(rule, "$") || strings.HasPrefix(rule, "{{") {
		return true
	}
	if strings.Contains(rule, "{{") || strings.Contains(rule, "@get:{") {
		return true
	}
	if len(rule) >= 2 && (rule[0] == '"' || rule[0] == '\'') && rule[len(rule)-1] == rule[0] {
		// 引号包裹的值剥引号后再判断（@put 值 JSON 引号剥去后是选择器）。
		return isRuleShapedValue(rule[1 : len(rule)-1])
	}
	return strings.Contains(rule, "@") || strings.Contains(rule, "{{")
}

// makeUpStep 对齐 Legado SourceRule.makeUpRule：在完整规则（含 ## 替换段）上一次性展开 @get:{}/{{}}，
// 再重新按 ## 拆分主规则与替换段。
func (a *Analyzer) makeUpStep(ctx context.Context, step sourceRule, input any) (sourceRule, error) {
	if !step.hasEval {
		return step, nil
	}
	// 标记 makeUpStep 已在完整 raw 上展开 @get/{{}}，evalStep 尾部 makeUpString 跳过
	// 重复展开（S06-07：含 {{}}/@get 的规则全串被展开两次的开销）。
	step.expanded = true
	var err error
	expanded := evalRulePattern.ReplaceAllStringFunc(step.raw, func(token string) string {
		if err != nil {
			return ""
		}
		if match := getRulePattern.FindStringSubmatch(token); len(match) == 2 && match[0] == token {
			return a.lookupVariable(input, strings.TrimSpace(match[1]))
		}
		expr := strings.TrimSpace(token[2 : len(token)-2])
		if isRuleExpression(expr) {
			var value string
			value, err = a.GetString(ctx, expr, input)
			return value
		}
		if a.ctx.Script == nil {
			// 对齐顶层模板的优雅回退（expandRawString/evalTemplateExpr）：无脚本运行时时
			// 保留原 token 求值结果为原文，不再让整条规则失败（Legado 内嵌 JS 引擎恒可用，
			// 未配置脚本运行时的部署下 {{js}} 模板不应使规则整体失败）。
			return token
		}
		var value any
		value, err = a.ctx.Script.Eval(ctx, expr, input)
		return scriptTemplateString(value)
	})
	if err != nil {
		return step, err
	}
	step.splitReplace(expanded)
	return step, nil
}

func isRuleExpression(expr string) bool {
	return strings.HasPrefix(expr, "@") ||
		strings.HasPrefix(expr, "$.") ||
		strings.HasPrefix(expr, "$[") ||
		strings.HasPrefix(expr, "$..") ||
		strings.HasPrefix(expr, "//")
}

func scriptTemplateString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%.0f", v)
		}
		return fmt.Sprint(v)
	default:
		return fmt.Sprint(v)
	}
}

func applyReplace(input any, step sourceRule) (any, error) {
	switch value := input.(type) {
	case []string:
		out := make([]string, 0, len(value))
		for _, item := range value {
			replaced, err := regex.Replace(item, step.replaceRegex, step.replacement, step.replaceFirst)
			if err != nil {
				return nil, err
			}
			out = append(out, replaced)
		}
		return out, nil
	case []any:
		// 逐项 asString 后逐项替换，避免整体 json.Marshal（S06-08：大 jsonpath 列表
		// 每经过一个正则/替换步骤就整体序列化一次的开销）。
		out := make([]any, 0, len(value))
		for _, item := range value {
			replaced, err := regex.Replace(asString(item), step.replaceRegex, step.replacement, step.replaceFirst)
			if err != nil {
				return nil, err
			}
			out = append(out, replaced)
		}
		return out, nil
	case string:
		return regex.Replace(value, step.replaceRegex, step.replacement, step.replaceFirst)
	default:
		return regex.Replace(asString(value), step.replaceRegex, step.replacement, step.replaceFirst)
	}
}

func asString(input any) string {
	switch value := input.(type) {
	case nil:
		return ""
	case *parsedInput:
		// 惰性解析载体对字符串消费者（正则步骤、替换、模板）等价于链首原始文本。
		return value.raw
	case string:
		return value
	case []byte:
		return string(value)
	case []string:
		return strings.Join(value, "\n")
	case map[string]any, []any, map[string]string, []map[string]any:
		raw, err := json.Marshal(value)
		if err == nil {
			return string(raw)
		}
		return fmt.Sprint(value)
	default:
		return fmt.Sprint(value)
	}
}

func collapseScalarResult(input any) any {
	switch value := input.(type) {
	case []string:
		if len(value) == 1 {
			return value[0]
		}
		return strings.Join(value, "\n")
	case []any:
		if len(value) == 1 {
			return collapseScalarResult(value[0])
		}
		return strings.Join(toStringList(value), "\n")
	default:
		return input
	}
}

func toStringList(input any) []string {
	return stringList(input, true)
}

func stringList(input any, normalizeCustomURL bool) []string {
	switch value := input.(type) {
	case nil:
		return nil
	case []string:
		return value
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil
		}
		if normalizeCustomURL {
			if parsed := urlrule.ParseCustom(trimmed); len(parsed.Attribute) > 0 {
				return []string{parsed.String()}
			}
		}
		return strings.Split(value, "\n")
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			out = append(out, asString(item))
		}
		return out
	default:
		return []string{asString(value)}
	}
}

func lookupInputField(input any, key string) (string, bool) {
	switch value := input.(type) {
	case map[string]any:
		item, ok := value[key]
		if !ok {
			return "", false
		}
		return asString(item), true
	case map[string]string:
		item, ok := value[key]
		return item, ok
	default:
		return "", false
	}
}

func (a *Analyzer) lookupVariable(input any, key string) string {
	// 对齐 Legado AnalyzeRule.get：bookName/title 优先取当前书籍名与章节标题。
	// S06-12：常见键用类型断言直取（替代反射 FieldByName），逐元素热点路径避免反射开销。
	switch key {
	case "bookName":
		if value, ok := carrierStringField(a.ctx.Book, "Name", "name"); ok {
			return value
		}
	case "title":
		if value, ok := carrierStringField(a.ctx.Chapter, "Title", "title"); ok {
			return value
		}
	}
	if value, ok := lookupInputField(input, key); ok && value != "" {
		return value
	}
	// 常见变量键先查 a.vars（Legado get 的 map 直查路径）。
	if value, ok := a.vars[key]; ok && value != "" {
		return value
	}
	if value, ok := lookupVariableFromContext(input, key); ok {
		return value
	}
	if value, ok := lookupVariableFromContext(nil, key, &a.ctx); ok {
		return value
	}
	return a.vars[key]
}

// carrierStringField 从书籍/章节载体读取字符串字段：结构体（或指针）取 field，map 取 mapKey；仅返回非空值。
func carrierStringField(carrier any, field string, mapKey string) (string, bool) {
	var value string
	switch typed := carrier.(type) {
	case nil:
		return "", false
	case map[string]any:
		raw, ok := typed[mapKey]
		if !ok || raw == nil {
			return "", false
		}
		value = asString(raw)
	case map[string]string:
		value = typed[mapKey]
	default:
		rv := reflect.ValueOf(carrier)
		if rv.Kind() == reflect.Pointer {
			if rv.IsNil() {
				return "", false
			}
			rv = rv.Elem()
		}
		if rv.Kind() != reflect.Struct {
			return "", false
		}
		fv := rv.FieldByName(field)
		if !fv.IsValid() || fv.Kind() != reflect.String {
			return "", false
		}
		value = fv.String()
	}
	return value, value != ""
}

func lookupVariableFromContext(input any, key string, ctx ...*Context) (string, bool) {
	var current *Context
	if len(ctx) > 0 {
		current = ctx[0]
	}
	for _, carrier := range variableCarriers(input, current) {
		if value, ok := lookupVariableFromCarrier(carrier, key); ok {
			return value, true
		}
	}
	return "", false
}

func lookupVariableFromCarrier(carrier any, key string) (string, bool) {
	switch typed := carrier.(type) {
	case map[string]any:
		if raw, ok := typed["variable"].(string); ok {
			if value, ok := decodeVariableEntry(raw, key); ok {
				return value, true
			}
		}
		if getter, ok := typed["getVariable"].(func(string) string); ok {
			value := getter(key)
			if strings.TrimSpace(value) != "" && !looksLikeVariablePayload(value) {
				return value, true
			}
		}
		if getter, ok := typed["getVariable"].(func(...any) string); ok {
			value := getter()
			if value, ok := decodeVariableEntry(value, key); ok {
				return value, true
			}
		}
	case map[string]string:
		if raw, ok := typed["variable"]; ok {
			if value, ok := decodeVariableEntry(raw, key); ok {
				return value, true
			}
		}
	default:
		if value, ok := lookupVariableFromStructCarrier(carrier, key); ok {
			return value, true
		}
	}
	return "", false
}

func lookupVariableFromStructCarrier(carrier any, key string) (string, bool) {
	// S06-12：domain 载体类型断言直取 Variable 字段，避免反射 FieldByName（热点路径）。
	switch typed := carrier.(type) {
	case *domainbook.Book:
		return decodeVariableEntry(typed.Variable, key)
	case *domainbook.BookChapter:
		return decodeVariableEntry(typed.Variable, key)
	}
	rv := reflect.ValueOf(carrier)
	if !rv.IsValid() {
		return "", false
	}
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "", false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return "", false
	}
	field := rv.FieldByName("Variable")
	if !field.IsValid() || field.Kind() != reflect.String {
		return "", false
	}
	return decodeVariableEntry(field.String(), key)
}

func decodeVariableEntry(raw string, key string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	values := decodeVariables(raw)
	if len(values) == 0 {
		return "", false
	}
	value, ok := values[key]
	if !ok || value == "" {
		return "", false
	}
	return value, true
}

func (a *Analyzer) storeVariable(input any, key string, value string) {
	for _, carrier := range variableCarriers(input, &a.ctx) {
		if storeVariableOnCarrier(carrier, key, value) {
			return
		}
	}
}

func variableCarriers(input any, ctx *Context) []any {
	carriers := make([]any, 0, 3)
	if container, ok := input.(map[string]any); ok {
		for _, name := range []string{"chapter", "book", "source"} {
			if raw, ok := container[name]; ok && raw != nil {
				carriers = append(carriers, raw)
			}
		}
	}
	if ctx != nil {
		for _, raw := range []any{ctx.Chapter, ctx.Book, ctx.Source} {
			if raw != nil {
				carriers = append(carriers, raw)
			}
		}
	}
	return carriers
}

func storeVariableOnCarrier(carrier any, key string, value string) bool {
	switch typed := carrier.(type) {
	case map[string]any:
		if setter, ok := typed["putVariable"].(func(string, any) string); ok {
			setter(key, value)
			if raw, ok := typed["variable"].(string); ok {
				vars := decodeVariables(raw)
				if vars == nil {
					vars = map[string]string{}
				}
				vars[key] = value
				if encoded, err := json.Marshal(vars); err == nil {
					typed["variable"] = string(encoded)
				}
			}
			return true
		}
		if setter, ok := typed["putVariable"].(func(string, any) bool); ok {
			setter(key, value)
			return true
		}
		if setter, ok := typed["setVariable"].(func(string) bool); ok {
			vars := map[string]string{}
			if raw, ok := typed["variable"].(string); ok {
				vars = decodeVariables(raw)
			}
			if vars == nil {
				vars = map[string]string{}
			}
			vars[key] = value
			if encoded, err := json.Marshal(vars); err == nil {
				setter(string(encoded))
				typed["variable"] = string(encoded)
				return true
			}
		}
	case map[string]string:
		vars := decodeVariables(typed["variable"])
		if vars == nil {
			vars = map[string]string{}
		}
		vars[key] = value
		if encoded, err := json.Marshal(vars); err == nil {
			typed["variable"] = string(encoded)
			return true
		}
	default:
		return storeVariableOnStructCarrier(carrier, key, value)
	}
	return false
}

func storeVariableOnStructCarrier(carrier any, key string, value string) bool {
	rv := reflect.ValueOf(carrier)
	if !rv.IsValid() || rv.Kind() != reflect.Pointer || rv.IsNil() {
		return false
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return false
	}
	field := elem.FieldByName("Variable")
	if !field.IsValid() || field.Kind() != reflect.String || !field.CanSet() {
		return false
	}
	vars := decodeVariables(field.String())
	if vars == nil {
		vars = map[string]string{}
	}
	vars[key] = value
	if encoded, err := json.Marshal(vars); err == nil {
		field.SetString(string(encoded))
		return true
	}
	return false
}

func looksLikeVariablePayload(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}")
}

func groupsToAny(groups [][]string) []any {
	out := make([]any, 0, len(groups))
	for _, group := range groups {
		out = append(out, group)
	}
	return out
}

func nodesToAny[T any](nodes []T) []any {
	out := make([]any, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node)
	}
	return out
}

func selectionToAny(selection *goquery.Selection) []any {
	out := []any{}
	selection.Each(func(_ int, item *goquery.Selection) {
		out = append(out, item)
	})
	return out
}
