package parser

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

var ErrSyntax = errors.New("rule syntax error")

type Step struct {
	Raw string
}

type Chain struct {
	Steps     []Step
	Separator string
}

func Split(rule string, separators ...string) (Chain, error) {
	if len(separators) == 0 {
		separators = []string{"@", "&&", "||", "%%"}
	}
	if err := validateBalanced(rule); err != nil {
		return Chain{}, err
	}
	parts := []Step{}
	current := strings.Builder{}
	chosenSeparator := ""
	for i := 0; i < len(rule); {
		if sep := matchSeparator(rule, i, separators, chosenSeparator); sep != "" {
			if chosenSeparator == "" {
				chosenSeparator = sep
			}
			parts = append(parts, Step{Raw: strings.TrimSpace(current.String())})
			current.Reset()
			i += len(sep)
			continue
		}
		r, width := nextRune(rule, i)
		current.WriteRune(r)
		i += width
		// 引号外 `\` 与 validateBalanced 对齐：转义下一 rune（S06-11：`a\&&b` 不切分）。
		if r == '\\' {
			_, escapedWidth := nextRune(rule, i)
			current.WriteString(rule[i : i+escapedWidth])
			i += escapedWidth
			continue
		}
		if r == '\'' || r == '"' {
			end, quoted, err := consumeQuoted(rule, i, r)
			if err != nil {
				// S07-04：未闭合引号降级为字面量继续（对齐 Legado RuleAnalyzer 容错），
				// 不再让整条选择器链失败。
				current.WriteString(quoted)
				i = end
				continue
			}
			current.WriteString(quoted)
			i = end
			continue
		}
		if r == '(' || r == '[' || r == '{' {
			end, balanced, err := consumeBalanced(rule, i, r, closeFor(r))
			if err != nil {
				// 平衡组未闭合时按字面量继续消费剩余文本（对齐 Legado 容错）。
				current.WriteString(balanced)
				i = end
			} else {
				current.WriteString(balanced)
				i = end
			}
		}
	}
	parts = append(parts, Step{Raw: strings.TrimSpace(current.String())})
	return Chain{Steps: parts, Separator: chosenSeparator}, nil
}

func matchSeparator(rule string, pos int, separators []string, chosen string) string {
	if chosen != "" {
		if strings.HasPrefix(rule[pos:], chosen) {
			return chosen
		}
		return ""
	}
	for _, sep := range separators {
		if sep == "" {
			continue
		}
		if strings.HasPrefix(rule[pos:], sep) {
			return sep
		}
	}
	return ""
}

func validateBalanced(rule string) error {
	stack := []rune{}
	for i := 0; i < len(rule); {
		r, width := nextRune(rule, i)
		i += width
		if r == '\\' {
			_, width := nextRune(rule, i)
			i += width
			continue
		}
		if r == '\'' || r == '"' {
			end, _, err := consumeQuoted(rule, i, r)
			if err != nil {
				// S07-04：未闭合引号容错继续（对齐 Legado RuleAnalyzer），仅平衡括号校验失败报错。
				continue
			}
			i = end
			continue
		}
		switch r {
		case '(', '[', '{':
			stack = append(stack, r)
		case ')', ']', '}':
			if len(stack) == 0 || closeFor(stack[len(stack)-1]) != r {
				return fmt.Errorf("%w: unexpected %q", ErrSyntax, r)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) != 0 {
		return fmt.Errorf("%w: unbalanced %q", ErrSyntax, stack[len(stack)-1])
	}
	return nil
}

func consumeQuoted(rule string, pos int, quote rune) (int, string, error) {
	start := pos
	for pos < len(rule) {
		r, width := nextRune(rule, pos)
		pos += width
		if r == '\\' {
			if pos >= len(rule) {
				break
			}
			_, escapedWidth := nextRune(rule, pos)
			pos += escapedWidth
			continue
		}
		if r == quote {
			return pos, rule[start:pos], nil
		}
	}
	return pos, rule[start:pos], fmt.Errorf("%w: unclosed quote %q", ErrSyntax, quote)
}

func consumeBalanced(rule string, pos int, open rune, close rune) (int, string, error) {
	start := pos
	depth := 1
	for pos < len(rule) {
		r, width := nextRune(rule, pos)
		pos += width
		if r == '\\' {
			if pos >= len(rule) {
				break
			}
			_, escapedWidth := nextRune(rule, pos)
			pos += escapedWidth
			continue
		}
		if r == '\'' || r == '"' {
			end, _, err := consumeQuoted(rule, pos, r)
			if err != nil {
				return pos, rule[start:pos], err
			}
			pos = end
			continue
		}
		if r == open {
			depth++
			continue
		}
		if r == close {
			depth--
			if depth == 0 {
				return pos, rule[start:pos], nil
			}
		}
	}
	return pos, rule[start:pos], fmt.Errorf("%w: unbalanced %q", ErrSyntax, open)
}

func closeFor(open rune) rune {
	switch open {
	case '(':
		return ')'
	case '[':
		return ']'
	case '{':
		return '}'
	default:
		return 0
	}
}

func nextRune(s string, pos int) (rune, int) {
	r, width := utf8.DecodeRuneInString(s[pos:])
	if r == utf8.RuneError && width == 0 {
		return utf8.RuneError, 1
	}
	return r, width
}
