package replaceapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"reader/internal/domain/replace"
	ruleregex "reader/internal/infra/rule/regex"
)

const maxRegexInputBytes = 2 * 1024 * 1024

var ErrRegexLimitExceeded = errors.New("replace_regex_limit_exceeded")

func plainReplace(text string, pattern string, replacement string) string {
	return strings.ReplaceAll(text, pattern, replacement)
}

func replaceText(ctx context.Context, rule replace.ReplaceRule, text string) (string, error) {
	if err := rule.Validate(); err != nil {
		return "", err
	}
	if !rule.IsRegex {
		return plainReplace(text, rule.Pattern, rule.Replacement), nil
	}
	if len(text) > maxRegexInputBytes {
		return "", fmt.Errorf("%w: input is %d bytes, limit is %d bytes", ErrRegexLimitExceeded, len(text), maxRegexInputBytes)
	}
	if err := ctx.Err(); err != nil {
		return "", errors.Join(ErrRegexLimitExceeded, err)
	}
	timeout := time.Duration(rule.ValidTimeoutMillisecond()) * time.Millisecond
	limitedCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := ruleregex.Replace(text, rule.Pattern, rule.Replacement, false)
	if err != nil {
		return "", err
	}
	if err := limitedCtx.Err(); err != nil {
		return "", errors.Join(ErrRegexLimitExceeded, err)
	}
	return result, nil
}
