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

	"github.com/PuerkitoBio/goquery"

	domainrule "reader/internal/domain/rule"
	"reader/internal/infra/rule/css"
	"reader/internal/infra/rule/jsonpath"
	"reader/internal/infra/rule/regex"
	urlrule "reader/internal/infra/rule/url"
	"reader/internal/infra/rule/xpath"
)

var (
	ErrUnsupportedContent = errors.New("unsupported rule content")
	ErrScriptUnavailable  = errors.New("script runtime is not configured")
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
}

func NewAnalyzer(ctx Context) *Analyzer {
	return &Analyzer{ctx: ctx, vars: map[string]string{}}
}

func (a *Analyzer) GetString(ctx context.Context, rule string, input any) (string, error) {
	if strings.TrimSpace(rule) == "" {
		return "", nil
	}
	if shouldShortCircuitTemplateRule(rule) {
		return a.renderTemplate(ctx, rule, input)
	}
	if strings.Contains(rule, "{$.") && !strings.Contains(rule, "{{") {
		return a.renderJSONPathTemplate(ctx, rule, input)
	}
	steps := splitSourceRules(rule, false)
	var current any = input
	for _, step := range steps {
		value, err := a.evalStep(ctx, step, current, false)
		if err != nil {
			return "", err
		}
		current = collapseScalarResult(value)
	}
	values := toStringList(current)
	if a.ctx.RedirectURL != "" {
		for i, value := range values {
			if shouldNormalizeURL(value) {
				values[i] = urlrule.Absolute(a.ctx.RedirectURL, value)
			}
		}
	}
	if len(values) == 0 {
		return "", nil
	}
	return strings.Join(values, "\n"), nil
}

func (a *Analyzer) ExpandString(ctx context.Context, text string, input any) (string, error) {
	return a.expandRawString(ctx, text, input)
}

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
	steps := splitSourceRules(rule, false)
	var current any = input
	for _, step := range steps {
		value, err := a.evalStep(ctx, step, current, false)
		if err != nil {
			return nil, err
		}
		current = collapseScalarResult(value)
	}
	values := stringList(current, normalizeURLs)
	if normalizeURLs && a.ctx.RedirectURL != "" {
		for i, value := range values {
			if shouldNormalizeURL(value) {
				values[i] = urlrule.Absolute(a.ctx.RedirectURL, value)
			}
		}
	}
	return values, nil
}

func (a *Analyzer) GetElements(ctx context.Context, rule string, input any) ([]any, error) {
	if strings.TrimSpace(rule) == "" {
		return []any{}, nil
	}
	steps := splitSourceRules(rule, true)
	var current any = input
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
		result, err = a.ctx.Script.Eval(ctx, step.rule, input)
	case ModeJSON:
		adapter, e := jsonpath.New(asString(input))
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
		adapter, e := xpath.New(asString(input))
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
		if wantElements {
			groups, e := adapter.Elements(asString(input), patterns...)
			if e != nil {
				return nil, e
			}
			result = groupsToAny(groups)
		} else {
			groups, e := adapter.Element(asString(input), patterns...)
			if e != nil {
				return nil, e
			}
			result = groups
		}
	default:
		if isJSONInput(input) {
			adapter, e := jsonpath.New(asString(input))
			if e != nil {
				return nil, e
			}
			jsonRule := normalizeJSONRule(step.rule)
			if wantElements {
				result, err = adapter.List(jsonRule)
			} else {
				result, err = adapter.Strings(jsonRule)
			}
			break
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
	text = getRulePattern.ReplaceAllStringFunc(text, func(token string) string {
		match := getRulePattern.FindStringSubmatch(token)
		if len(match) != 2 {
			return token
		}
		key := strings.TrimSpace(match[1])
		if value, ok := lookupInputField(input, key); ok {
			return value
		}
		return a.lookupVariable(input, key)
	})
	text = jsRulePattern.ReplaceAllStringFunc(text, func(token string) string {
		match := jsRulePattern.FindStringSubmatch(token)
		if len(match) != 2 {
			return token
		}
		expr := strings.TrimSpace(match[1])
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
		match := jsonTplPattern.FindStringSubmatch(token)
		if len(match) != 2 {
			return token
		}
		var value string
		value, err = a.GetString(ctx, "$"+match[1], input)
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
	return css.New(asString(input))
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
	putMap       map[string]string
	literal      bool
}

var (
	putRulePattern = regexp.MustCompile(`(?i)@put:(\{[^}]+?\})`)
	getRulePattern = regexp.MustCompile(`(?i)@get:\{([^}]+?)\}`)
	jsRulePattern  = regexp.MustCompile(`(?is)\{\{([\w\W]*?)\}\}`)
	jsonTplPattern = regexp.MustCompile(`(?s)\{\$(\.[^{}]+?)\}`)
)

func splitSourceRules(rule string, allInOne bool) []sourceRule {
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

func parseSourceRule(raw string, mode Mode) sourceRule {
	rule := strings.TrimSpace(raw)
	switch {
	case mode == ModeRegex:
	case strings.HasPrefix(strings.ToLower(rule), "@js:"):
		mode = ModeJS
		rule = strings.TrimSpace(rule[4:])
	case strings.HasPrefix(strings.ToLower(rule), "<js>") && strings.HasSuffix(strings.ToLower(rule), "</js>"):
		mode = ModeJS
		rule = strings.TrimSpace(rule[4 : len(rule)-5])
	case strings.HasPrefix(strings.ToLower(rule), "@xpath:"):
		mode = ModeXPath
		rule = strings.TrimSpace(rule[7:])
	case strings.HasPrefix(strings.ToLower(rule), "@css:"):
		rule = strings.TrimSpace(rule[5:])
	case strings.HasPrefix(strings.ToLower(rule), "@json:"):
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
	putMap := parsePutRules(rule)
	rule = putRulePattern.ReplaceAllString(rule, "")
	parts := strings.Split(rule, "##")
	step := sourceRule{
		rule:    strings.TrimSpace(parts[0]),
		mode:    mode,
		putMap:  putMap,
		literal: getRulePattern.MatchString(rule) || jsRulePattern.MatchString(rule),
	}
	if len(parts) > 1 {
		step.replaceRegex = parts[1]
	}
	if len(parts) > 2 {
		step.replacement = parts[2]
	}
	if len(parts) > 3 {
		step.replaceFirst = true
	}
	return step
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

func parsePutRules(rule string) map[string]string {
	matches := putRulePattern.FindAllStringSubmatch(rule, -1)
	if len(matches) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, match := range matches {
		for key, value := range parseRuleObject(match[1]) {
			out[key] = value
		}
	}
	return out
}

func isJSONInput(input any) bool {
	text := strings.TrimSpace(asString(input))
	return (strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}")) ||
		(strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]"))
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

func parseRuleObject(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	strict := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &strict); err == nil {
		return strict
	}
	raw = strings.TrimPrefix(strings.TrimSuffix(raw, "}"), "{")
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			out[key] = value
		}
	}
	return out
}

func (a *Analyzer) putStepVars(ctx context.Context, step sourceRule, input any) error {
	for key, rule := range step.putMap {
		value, err := a.evalPutRule(ctx, rule, input)
		if err != nil {
			return err
		}
		a.storeVariable(input, key, value)
		a.vars[key] = value
	}
	return nil
}

func (a *Analyzer) evalPutRule(ctx context.Context, rule string, input any) (string, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return "", nil
	}
	if value, ok := lookupInputField(input, rule); ok {
		return value, nil
	}
	value, err := a.GetString(ctx, rule, input)
	if err != nil {
		return "", err
	}
	return value, nil
}

func (a *Analyzer) makeUpStep(ctx context.Context, step sourceRule, input any) (sourceRule, error) {
	var err error
	step.rule = getRulePattern.ReplaceAllStringFunc(step.rule, func(token string) string {
		if err != nil {
			return ""
		}
		match := getRulePattern.FindStringSubmatch(token)
		if len(match) != 2 {
			return ""
		}
		return a.lookupVariable(input, strings.TrimSpace(match[1]))
	})
	step.rule = jsRulePattern.ReplaceAllStringFunc(step.rule, func(token string) string {
		if err != nil {
			return ""
		}
		match := jsRulePattern.FindStringSubmatch(token)
		if len(match) != 2 {
			return ""
		}
		expr := strings.TrimSpace(match[1])
		if isRuleExpression(expr) {
			var value string
			value, err = a.GetString(ctx, expr, input)
			return value
		}
		if a.ctx.Script == nil {
			err = ErrScriptUnavailable
			return ""
		}
		var value any
		value, err = a.ctx.Script.Eval(ctx, expr, input)
		return scriptTemplateString(value)
	})
	if err != nil {
		return step, err
	}
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
		return []string{html.UnescapeString(asString(value))}
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
	if value, ok := lookupInputField(input, key); ok && value != "" {
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
		return lookupVariableFromStructCarrier(carrier, key)
	}
	return "", false
}

func lookupVariableFromStructCarrier(carrier any, key string) (string, bool) {
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

func shouldNormalizeURL(value string) bool {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if value == "" || strings.ContainsAny(value, " \t\r\n<>") {
		return false
	}
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "data:") ||
		strings.HasPrefix(lower, "javascript") ||
		strings.HasPrefix(value, "/") ||
		strings.HasPrefix(value, "./") ||
		strings.HasPrefix(value, "../") ||
		strings.Contains(value, ".")
}
