package bookapp

import (
	"regexp"
	"strconv"
	"strings"
)

// 本文件对齐 Legado 搜索/发现/详情结果字段的展示清洗：
// 书名与作者按 AppPattern 去掉装饰（BookHelp.formatBookName/formatBookAuthor），
// 字数按 StringUtils.wordCountFormat 换算，简介按 HtmlFormatter.format 去 HTML 标签。

var (
	// bookNameDecoratePattern 对齐 AppPattern.nameRegex：去掉 " 作者xxx" 与 " xxx 著" 装饰。
	bookNameDecoratePattern = regexp.MustCompile(`\s+作\s*者.*|\s+\S+\s+著`)
	// bookAuthorDecoratePattern 对齐 AppPattern.authorRegex：去掉开头的 "作者:" 与结尾的 " 著"。
	bookAuthorDecoratePattern = regexp.MustCompile(`^\s*作\s*者[:：\s]+|\s+著`)
	// wordCountNumberPattern 对齐 StringUtils.isNumeric：仅整串为可选负号的十进制数字时才算数值。
	wordCountNumberPattern = regexp.MustCompile(`^-?[0-9]+$`)

	htmlNbspPattern    = regexp.MustCompile(`(&nbsp;)+`)
	htmlEnSpacePattern = regexp.MustCompile(`(&ensp;|&emsp;)`)
	htmlNoPrintPattern = regexp.MustCompile(`(&thinsp;|&zwnj;|&zwj;|\x{2009}|\x{200C}|\x{200D})`)
	htmlWrapPattern    = regexp.MustCompile(`</?(?:div|p|br|hr|h\d|article|dd|dl)[^>]*>`)
	htmlCommentPattern = regexp.MustCompile(`<!--[^>]*-->`)
	// Go regexp 不支持前瞻，用备选分支等价表达 Legado 的 `</?[a-zA-Z]+(?=[ >])[^<>]*>`。
	htmlTagPattern        = regexp.MustCompile(`</?[a-zA-Z]+[ >][^<>]*>|</?[a-zA-Z]+>`)
	htmlIndentPattern     = regexp.MustCompile(`\s*\n+\s*`)
	htmlLeadIndentPattern = regexp.MustCompile(`^[\n\s]+`)
	htmlTrailIndentPatn   = regexp.MustCompile(`[\n\s]+$`)
)

// FormatBookName 对齐 Legado BookHelp.formatBookName。
func FormatBookName(name string) string {
	return trimBelowSpace(bookNameDecoratePattern.ReplaceAllString(name, ""))
}

// FormatBookAuthor 对齐 Legado BookHelp.formatBookAuthor。
func FormatBookAuthor(author string) string {
	return trimBelowSpace(bookAuthorDecoratePattern.ReplaceAllString(author, ""))
}

// FormatWordCount 对齐 Legado StringUtils.wordCountFormat：数值超过一万时换算为 "x.x万字"，
// 数值但非正数返回空串，非数值原样返回。
func FormatWordCount(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if !wordCountNumberPattern.MatchString(value) {
		return value
	}
	words, err := strconv.Atoi(value)
	if err != nil {
		return value
	}
	if words <= 0 {
		return ""
	}
	if words > 10000 {
		return formatTenThousand(words) + "万字"
	}
	return strconv.Itoa(words) + "字"
}

// formatTenThousand 对齐 DecimalFormat("#.#")：保留一位小数并去掉多余的 ".0"。
func formatTenThousand(words int) string {
	text := strconv.FormatFloat(float64(words)/10000, 'f', 1, 64)
	return strings.TrimSuffix(text, ".0")
}

// FormatIntro 对齐 Legado HtmlFormatter.format：常量实体转换、块级标签转换行、去注释与标签并缩进。
func FormatIntro(html string) string {
	if html == "" {
		return ""
	}
	out := htmlNbspPattern.ReplaceAllString(html, " ")
	out = htmlEnSpacePattern.ReplaceAllString(out, " ")
	out = htmlNoPrintPattern.ReplaceAllString(out, "")
	out = htmlWrapPattern.ReplaceAllString(out, "\n")
	out = htmlCommentPattern.ReplaceAllString(out, "")
	out = htmlTagPattern.ReplaceAllString(out, "")
	out = htmlIndentPattern.ReplaceAllString(out, "\n　　")
	out = htmlLeadIndentPattern.ReplaceAllString(out, "　　")
	out = htmlTrailIndentPatn.ReplaceAllString(out, "")
	return out
}

// trimBelowSpace 对齐 Kotlin 的 trim { it <= ' ' }：去掉首尾所有 ASCII 控制字符与空格（含全角空格）。
func trimBelowSpace(value string) string {
	return strings.TrimFunc(value, func(r rune) bool { return r <= ' ' })
}
