package replaceapp

import (
	"context"
	"errors"
	"strings"
	"time"

	"reader/internal/domain/replace"
	ruleregex "reader/internal/infra/rule/regex"
)

const maxRegexInputBytes = 2 * 1024 * 1024

// 对齐 Legado ContentProcessor.kt:160-180 的错误分类：
//   - ErrRegexTimeout：正则执行超时（对应 RegexTimeoutException，仅此分支禁用规则）；
//   - ErrRegexInputTooLarge：输入超过 maxRegexInputBytes（Legado 无此概念，输入大小限制
//     与规则本身合法性无关，不得因此禁用规则）；
//   - ErrRegexCancelled：请求上下文取消/超期（对应 CancellationException，静默跳过）。
var (
	ErrRegexTimeout       = errors.New("replace regex timeout")
	ErrRegexInputTooLarge = errors.New("replace regex input too large")
	ErrRegexCancelled     = errors.New("replace regex cancelled")
)

// ErrRegexLimitExceeded 保留为三者的聚合哨兵：既有调用方（content 层、CLI、API）用
// errors.Is 判断"这条规则因限流类原因失败"，具体分类再按上方哨兵细分。
var ErrRegexLimitExceeded = errors.Join(ErrRegexTimeout, ErrRegexInputTooLarge, ErrRegexCancelled)

// regexFailureClass 是 replaceText 失败的细分类型；errors.Join 的聚合哨兵会让
// errors.Is(err, ErrRegexTimeout) 对任何聚合都成立，因此分类判断走显式类型而非哨兵。
type regexFailureClass int

const (
	regexFailureNone regexFailureClass = iota
	regexFailureTimeout
	regexFailureInputTooLarge
	regexFailureCancelled
	regexFailureOther
)

// classifyRegexFailure 返回 err 的细分类型：replaceText 内部直接按构造处标注；
// 外部错误（Validate 失败、正则执行错误）归为 Other。
func classifyRegexFailure(err error) regexFailureClass {
	if err == nil {
		return regexFailureNone
	}
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, ErrRegexInputTooLarge.Error()):
		return regexFailureInputTooLarge
	case strings.HasPrefix(msg, ErrRegexTimeout.Error()):
		return regexFailureTimeout
	case strings.HasPrefix(msg, ErrRegexCancelled.Error()):
		return regexFailureCancelled
	default:
		return regexFailureOther
	}
}

func plainReplace(text string, pattern string, replacement string) string {
	return strings.ReplaceAll(text, pattern, replacement)
}

func replaceText(ctx context.Context, rule replace.ReplaceRule, text string) (string, error) {
	// 对齐 S14-12：编译检查走 infra/rule/regex 的共享 LRU（CompileStd），命中后同一 pattern
	// 每章只编译一次；不再调用 rule.Validate()（其每次 regexp.Compile 完全绕过缓存）。
	// 平文本规则无正则可编译，直接沿用 Validate 的空 pattern 检查。
	if !rule.IsRegex {
		if rule.Pattern == "" {
			return "", replace.ErrEmptyPattern
		}
		return plainReplace(text, rule.Pattern, rule.Replacement), nil
	}
	if rule.Pattern == "" {
		return "", replace.ErrEmptyPattern
	}
	if _, err := ruleregex.CompileStd(rule.Pattern); err != nil {
		return "", errors.Join(replace.ErrInvalidRegex, err)
	}
	if len(rule.Pattern) > 0 && rule.Pattern[len(rule.Pattern)-1] == '|' && (len(rule.Pattern) < 2 || rule.Pattern[len(rule.Pattern)-2] != '\\') {
		return "", replace.ErrInvalidRegex
	}
	if len(text) > maxRegexInputBytes {
		// 输入超大与规则本身无关，直接返回独立错误，不触碰规则状态（S14-01）。
		return "", errors.Join(ErrRegexInputTooLarge, ErrRegexLimitExceeded)
	}
	if err := ctx.Err(); err != nil {
		// 请求取消不属于正则超时，不得禁用规则（对齐 Legado 的 CancellationException 分支）。
		return "", errors.Join(ErrRegexCancelled, ErrRegexLimitExceeded, err)
	}
	timeout := time.Duration(rule.ValidTimeoutMillisecond()) * time.Millisecond
	limitedCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := ruleregex.Replace(text, rule.Pattern, rule.Replacement, false)
	if err != nil {
		return "", err
	}
	// 仅"执行未先完成而 limitedCtx 已过期"才算正则超时，对应 Legado RegexTimeoutException。
	if ctx.Err() == nil && limitedCtx.Err() != nil {
		return "", errors.Join(ErrRegexTimeout, ErrRegexLimitExceeded)
	}
	return result, nil
}
