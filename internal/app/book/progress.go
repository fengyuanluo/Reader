package bookapp

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"reader/internal/domain/book"
)

// 本文件移植 legado/app/src/main/java/io/legado/app/help/book/BookHelp.kt:499-546 的
// getDurChapter 与配套章节名工具（getChapterNum/getPureChapterName）：
// 目录刷新后按标题 Jaccard 相似度与章节序号重映射旧阅读进度，避免章节
// 重排（插入新章、`-` 倒序、去重删除）后 dur_chapter_index 越界或指向错误章节。

var (
	// chapterNumPattern1 对齐 BookHelp.chapterNamePattern1：从 “第…章/节/篇/回/集/话” 中提取序号。
	chapterNumPattern1 = regexp.MustCompile(`.*?第([\d零〇一二两三四五六七八九十百千万壹贰叁肆伍陆柒捌玖拾佰仟]+)[章节篇回集话]`)
	// chapterNumPattern2 对齐 BookHelp.chapterNamePattern2：从 “1,2、xxx” 形式的序号列表中提取最后一个序号。
	// Go regexp 不支持 (?<=) 等价的后瞻，Legado 的 `\.[^\d]` 改为捕获时排除结尾小数（见 getChapterNum 的预处理）。
	chapterNumPattern2 = regexp.MustCompile(`^(?:[\d零〇一二两三四五六七八九十百千万壹贰叁肆伍陆柒捌玖拾佰仟]+[,:、])*([\d零〇一二两三四五六七八九十百千万壹贰叁肆伍陆柒捌玖拾佰仟]+)(?:[,:、]|\.[^\d])`)
	// chapterNumPattern2Trailing 与上一条相同，但以数字结尾（Legado `\.(?=[^\d])` 在结尾数字时整体不匹配，
	// 退回 pattern1；这里单独列出供结尾判定）。
	chapterNumPattern2Trailing = regexp.MustCompile(`^(?:[\d零〇一二两三四五六七八九十百千万壹贰叁肆伍陆柒捌玖拾佰仟]+[,:、])*([\d零〇一二两三四五六七八九十百千万壹贰叁肆伍陆柒捌玖拾佰仟]+)$`)
	// pureChapterNamePatternB 对齐 BookHelp.regexB：剔除章节序号前缀，但排除处于结尾的状况。
	// Go regexp 不支持前瞻 (?!$)，用两个备选分支等价表达：
	// 分支 1 匹配序号后还有内容（非结尾）的 “第N章” 前缀；分支 2 匹配序号列表 + 序号后
	// 还有内容的形式。结尾序号（regexB 的 (?!$) 排除项）两个分支都不匹配，被保留。
	pureChapterNamePatternB1 = regexp.MustCompile(`^.*?第[\d零〇一二两三四五六七八九十百千万壹贰叁肆伍陆柒捌玖拾佰仟]+[章节篇回集话]`)
	pureChapterNamePatternB2 = regexp.MustCompile(`^(?:[\d零〇一二两三四五六七八九十百千万壹贰叁肆伍陆柒捌玖拾佰仟]+[,:、])*(?:[\d零〇一二两三四五六七八九十百千万壹贰叁肆伍陆柒捌玖拾佰仟]+)(?:[,:、]|\.[^\d])`)
	// pureChapterNamePatternC 对齐 BookHelp.regexC：剔除前后括号附加内容，
	// 整个章节名都在括号中时只剔除首尾括号。Go 不支持前瞻，改为替换后回读判定。
	pureChapterNamePatternCLeft  = regexp.MustCompile(`^[〖【《〔\[{(]`)
	pureChapterNamePatternCRight = regexp.MustCompile(`[)〕》】〗\]}]$`)
	pureChapterNamePatternCInner = regexp.MustCompile(`[〖【《〔\[{(][^〖【《〔\[{()〕》】〗\]}]+[)〕》】〗\]}]$`)
	// pureChapterNamePatternOther 对齐 BookHelp.regexOther：剔除所有非字母数字中日韩文字
	// （CJK 区+扩展 A-F 区）。Go 字符类用 \p{...} 与字面区间表达 \w 与 CJK。
	pureChapterNamePatternOther = regexp.MustCompile(`[^\p{L}\p{N}_\x{4E00}-\x{9FEF}〇\x{3400}-\x{4DBF}\x{20000}-\x{2A6DF}\x{2A700}-\x{2EBEF}]`)
	// whitespaceRun 对齐 BookHelp.regexA：所有空白。
	whitespaceRun = regexp.MustCompile(`\s+`)
)

// getDurChapter 返回旧进度在新章节列表中的下标（对齐 BookHelp.getDurChapter）。
// oldChapterListSize 为刷新前的章节总数，为 0 时按比例缩放分支退化为原下标。
func getDurChapter(oldDurChapterIndex int, oldDurChapterName string, newChapters []*book.BookChapter, oldChapterListSize int) int {
	if oldDurChapterIndex <= 0 {
		return 0
	}
	if len(newChapters) == 0 {
		return oldDurChapterIndex
	}
	oldChapterNum := getChapterNum(oldDurChapterName)
	oldName := getPureChapterName(oldDurChapterName)
	newChapterSize := len(newChapters)
	durIndex := oldDurChapterIndex
	if oldChapterListSize != 0 {
		durIndex = oldDurChapterIndex * oldChapterListSize / newChapterSize
	}
	min := int(math.Max(0, math.Min(float64(oldDurChapterIndex), float64(durIndex))-10))
	max := int(math.Min(float64(newChapterSize-1), math.Max(float64(oldDurChapterIndex), float64(durIndex))+10))
	nameSim := 0.0
	newIndex := 0
	newNum := 0
	if oldName != "" {
		for i := min; i <= max; i++ {
			newName := getPureChapterName(newChapters[i].Title)
			temp := jaccardSimilarity(oldName, newName)
			if temp > nameSim {
				nameSim = temp
				newIndex = i
			}
		}
	}
	if nameSim < 0.96 && oldChapterNum > 0 {
		for i := min; i <= max; i++ {
			temp := getChapterNum(newChapters[i].Title)
			if temp == oldChapterNum {
				newNum = temp
				newIndex = i
				break
			} else if math.Abs(float64(temp-oldChapterNum)) < math.Abs(float64(newNum-oldChapterNum)) {
				newNum = temp
				newIndex = i
			}
		}
	}
	if nameSim > 0.96 || math.Abs(float64(newNum-oldChapterNum)) < 1 {
		return newIndex
	}
	return int(math.Min(float64(newChapterSize-1), math.Max(0, float64(oldDurChapterIndex))))
}

// getChapterNum 对齐 BookHelp.getChapterNum：全角转半角去空白后按 pattern1/pattern2 提取序号，
// 未匹配返回 -1。
func getChapterNum(chapterName string) int {
	if chapterName == "" {
		return -1
	}
	normalized := whitespaceRun.ReplaceAllString(fullToHalf(chapterName), "")
	if match := chapterNumPattern1.FindStringSubmatch(normalized); match != nil {
		return stringToInt(match[1])
	}
	// pattern2 的 `\.[^\d]` 要求序号后的小数点不接数字；以数字结尾的串在 Legado 中不匹配 pattern2。
	if match := chapterNumPattern2.FindStringSubmatch(normalized); match != nil {
		return stringToInt(match[1])
	}
	if match := chapterNumPattern2Trailing.FindStringSubmatch(normalized); match != nil && !strings.Contains(normalized, ".") {
		return stringToInt(match[1])
	}
	return -1
}

// getPureChapterName 对齐 BookHelp.getPureChapterName：去空白、章节序号、括号附加与非文字字符。
func getPureChapterName(chapterName string) string {
	if chapterName == "" {
		return ""
	}
	name := fullToHalf(chapterName)
	name = whitespaceRun.ReplaceAllString(name, "")
	// regexB 等价：只在序号后还有内容（非结尾）时剔除序号前缀。
	if name2 := pureChapterNamePatternB1.ReplaceAllString(name, ""); name2 != name && name2 != "" {
		name = name2
	} else if name2 := pureChapterNamePatternB2.ReplaceAllString(name, ""); name2 != name && name2 != "" {
		name = name2
	}
	name = stripDecorationBrackets(name)
	name = pureChapterNamePatternOther.ReplaceAllString(name, "")
	return name
}

// stripDecorationBrackets 对齐 regexC：先剔除非结尾的成对括号附加内容，
// 剩余首尾括号在整串都被括号包裹时只剔除首尾。
func stripDecorationBrackets(name string) string {
	if pureChapterNamePatternCInner.MatchString(name) {
		name = pureChapterNamePatternCInner.ReplaceAllString(name, "")
	}
	if pureChapterNamePatternCLeft.MatchString(name) {
		trimmed := name[1:]
		if pureChapterNamePatternCRight.MatchString(trimmed) && !pureChapterNamePatternCInner.MatchString(trimmed[:len(trimmed)-1]) {
			name = trimmed[:len(trimmed)-1]
		}
	}
	return name
}

// jaccardSimilarity 移植 org.apache.commons.text.similarity.JaccardSimilarity（按 rune 切分）：
// J = |A∩B| / |A∪B|，两串都为空返回 1（与 commons-text 对空集的定义一致退化为 0/0，这里取 1 保持
// 同名标题映射不动）。
func jaccardSimilarity(a string, b string) float64 {
	setA := runesOf(a)
	setB := runesOf(b)
	if len(setA) == 0 && len(setB) == 0 {
		return 1
	}
	intersection := 0
	for r := range setA {
		if _, ok := setB[r]; ok {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func runesOf(text string) map[rune]struct{} {
	set := make(map[rune]struct{}, len([]rune(text)))
	for _, r := range text {
		if !unicode.IsSpace(r) {
			set[r] = struct{}{}
		}
	}
	return set
}

// stringToInt/fullToHalf 与 internal/infra/script/extensions.go 的同名移植一致；
// script 包未导出它们，这里按 Legado StringUtils.stringToInt 重新实现（内部私有，避免跨包改动）。
func stringToInt(text string) int {
	num := whitespaceRun.ReplaceAllString(fullToHalf(text), "")
	if parsed, err := strconv.ParseInt(num, 10, 32); err == nil {
		return int(parsed)
	}
	return chineseNumToInt(num)
}

func fullToHalf(text string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == 12288:
			return 32
		case r >= 65281 && r <= 65374:
			return r - 65248
		default:
			return r
		}
	}, text)
}

// chineseNumToInt 移植 StringUtils.chineseNumToInt；含未知字符时返回 -1。
func chineseNumToInt(text string) int {
	if text == "" {
		return -1
	}
	result := 0
	section := 0
	for _, r := range text {
		value, ok := chineseNumValue(r)
		if !ok {
			return -1
		}
		switch {
		case value >= 10000:
			section = (section + result) * value
			result = 0
		case value >= 10:
			if section == 0 {
				section = value
			} else {
				section = (result + maxInt(section, 1)) * value
			}
			result = 0
		default:
			result = result*10 + value
		}
	}
	total := section + result
	if total == 0 {
		return -1
	}
	return total
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func chineseNumValue(r rune) (int, bool) {
	switch r {
	case '零', '〇':
		return 0, true
	case '一', '壹':
		return 1, true
	case '二', '两', '贰':
		return 2, true
	case '三', '叁':
		return 3, true
	case '四', '肆':
		return 4, true
	case '五', '伍':
		return 5, true
	case '六', '陆':
		return 6, true
	case '七', '柒':
		return 7, true
	case '八', '捌':
		return 8, true
	case '九', '玖':
		return 9, true
	case '十', '拾':
		return 10, true
	case '百', '佰':
		return 100, true
	case '千', '仟':
		return 1000, true
	case '万':
		return 10000, true
	}
	return 0, false
}
