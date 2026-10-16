package urlrule

import (
	"regexp"
	"strings"
)

// pageListPattern 对齐 Legado AnalyzeUrl.pagePattern：`<(.*?)>`，非贪婪匹配尖括号内的分页列表。
var pageListPattern = regexp.MustCompile(`<(.*?)>`)

// ReplacePagePlaceholders 对齐 Legado AnalyzeUrl.replaceKeyPageJs 的分页占位符：
// `<1,2,3>` 按当前页码取第 page 项（页码从 1 开始），页码超出列表长度时取末项。
// page 小于 1 时按 1 处理。
func ReplacePagePlaceholders(input string, page int) string {
	if page < 1 {
		page = 1
	}
	if !strings.Contains(input, "<") {
		return input
	}
	return pageListPattern.ReplaceAllStringFunc(input, func(match string) string {
		pages := strings.Split(match[1:len(match)-1], ",")
		if len(pages) == 0 {
			return match
		}
		index := page - 1
		if index >= len(pages) {
			index = len(pages) - 1
		}
		return strings.TrimFunc(pages[index], func(r rune) bool { return r <= ' ' })
	})
}
