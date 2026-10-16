package replaceapp

import (
	"hash/fnv"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	reSegmentWordMaxLength = 16

	reSegmentMarkSentencesEnd    = "？。！?!~"
	reSegmentMarkSentencesEndP   = ".？。！?!~"
	reSegmentMarkSentencesMid    = ".，、,—…"
	reSegmentMarkSentencesSay    = "问说喊唱叫骂道着答"
	reSegmentMarkQuotationBefore = "，：,:"
	reSegmentMarkQuotation       = "\"“”"
	reSegmentMarkQuotationRight  = "\"”"
)

var (
	reSegmentQuoteToLeft        = regexp.MustCompile(`&quot;`)
	reSegmentColonQuote         = regexp.MustCompile(`[:：]['"‘”“]+`)
	reSegmentDoubleQuote        = regexp.MustCompile(`["”“]+\s*["”“][\s"”“]*`)
	reSegmentSplitLines         = regexp.MustCompile(`\n(\s*)`)
	reSegmentInnerSpace         = regexp.MustCompile(`[　\s]+`)
	reSegmentLineSpace          = regexp.MustCompile(`[　\s]`)
	reSegmentQuotePairs         = regexp.MustCompile(`["”“]+\s*["”“]+`)
	reSegmentQuoteEnd           = regexp.MustCompile(`["”“]+(？。！?!~)["”“]+`)
	reSegmentQuoteTail          = regexp.MustCompile(`["”“]+(？。！?!~)([^"”“])`)
	reSegmentSaidDot            = regexp.MustCompile(`([问说喊唱叫骂道着答])[\\.。]`)
	reSegmentQuoteNarrationCut  = regexp.MustCompile(`([。！？?!~]["”])([^“"\n]{1,24}(?:问|说|喊|唱|叫|骂|道|着|答)[^“"\n]{0,12}[:：]["“])`)
	reSegmentQuoteAfterNarrator = regexp.MustCompile(`([。！？?!~][”"])([^“"\n]{1,24})`)
	reSegmentTrimStart          = regexp.MustCompile(`^\s+`)
	reSegmentFinalQuoteSplit    = regexp.MustCompile(`\s*["”“]+\s*["”“][\s"”“]*`)
	reSegmentFinalColonQuote    = regexp.MustCompile(`[:：][”“"\s]+`)
	reSegmentFinalDialogueQuote = regexp.MustCompile(`\n["“”]([^\n"“”]+)([,:，：]["”“])([^\n"“”]+)`)
	reSegmentParagraphDialogue  = regexp.MustCompile(`^[\"”“][^\"”“]+[\"”“]$`)
	reSegmentDictWord           = regexp.MustCompile(`[\"'”“]([^\n\p{P}]{1,16})[\"'”“]`)
)

func reSegment(content string, chapterTitle string) string {
	content = strings.TrimSpace(content)
	if content == "" || content == "null" {
		return content
	}

	dict := makeReSegmentDict(content)
	paragraphs := reSegmentSplitLines.Split(
		reSegmentDoubleQuote.ReplaceAllString(
			reSegmentColonQuote.ReplaceAllString(
				reSegmentQuoteToLeft.ReplaceAllString(content, "“"),
				"：“",
			),
			"”\n“",
		),
		-1,
	)
	if len(paragraphs) == 0 {
		return content
	}

	var merged strings.Builder
	merged.Grow(int(float64(len(content)) * 1.15))
	merged.WriteString("  ")
	first := strings.TrimSpace(paragraphs[0])
	if first != strings.TrimSpace(chapterTitle) {
		merged.WriteString(reSegmentInnerSpace.ReplaceAllString(first, ""))
	}
	for i := 1; i < len(paragraphs); i++ {
		if shouldBreakMergedParagraphBuilder(&merged) {
			merged.WriteByte('\n')
		}
		merged.WriteString(reSegmentLineSpace.ReplaceAllString(paragraphs[i], ""))
	}

	preprocessed := reSegmentQuoteTail.ReplaceAllString(
		reSegmentQuoteEnd.ReplaceAllString(
			reSegmentQuotePairs.ReplaceAllString(merged.String(), "”\n“"),
			"”$1\n“",
		),
		"”$1\n$2",
	)
	preprocessed = reSegmentSaidDot.ReplaceAllString(preprocessed, "$1。\n")
	parts := strings.Split(preprocessed, "\n")

	var rebuilt strings.Builder
	rebuilt.Grow(int(float64(len(content)) * 1.15))
	for _, part := range parts {
		rebuilt.WriteByte('\n')
		rebuilt.WriteString(findReSegmentNewLines(part, dict))
	}

	reduced := reduceReSegmentLength(rebuilt.String())
	reduced = reSegmentTrimStart.ReplaceAllString(reduced, "")
	reduced = reSegmentFinalQuoteSplit.ReplaceAllString(reduced, "”\n“")
	reduced = reSegmentFinalColonQuote.ReplaceAllString(reduced, "：“")
	reduced = reSegmentQuoteNarrationCut.ReplaceAllString(reduced, "$1\n$2")
	reduced = reSegmentQuoteAfterNarrator.ReplaceAllString(reduced, "$1\n$2")
	reduced = reSegmentFinalDialogueQuote.ReplaceAllString(reduced, "\n$1：“$3")
	reduced = strings.ReplaceAll(reSegmentSplitLines.ReplaceAllString(reduced, "\n"), "\r", "")
	return reduced
}

func shouldBreakMergedParagraphBuilder(builder *strings.Builder) bool {
	text := strings.TrimRight(builder.String(), "\r\n")
	if text == "" {
		return false
	}
	runes := []rune(text)
	last := runes[len(runes)-1]
	if matchReSegmentRune(reSegmentMarkSentencesEnd, last) {
		return true
	}
	return matchReSegmentRune(reSegmentMarkQuotationRight, last) &&
		len(runes) >= 2 &&
		matchReSegmentRune(reSegmentMarkSentencesEnd, runes[len(runes)-2])
}

func reduceReSegmentLength(input string) string {
	paragraphs := strings.Split(input, "\n")
	if len(paragraphs) == 0 {
		return input
	}
	dialogueFlags := make([]bool, len(paragraphs))
	for i, paragraph := range paragraphs {
		dialogueFlags[i] = reSegmentParagraphDialogue.MatchString(paragraph)
	}
	dialogue := 0
	for i := range paragraphs {
		if dialogueFlags[i] {
			if dialogue < 0 {
				dialogue = 1
			} else if dialogue < 2 {
				dialogue++
			}
			continue
		}
		if dialogue > 1 {
			paragraphs[i] = splitReSegmentQuote(paragraphs[i])
			dialogue--
		} else if dialogue > 0 && i < len(paragraphs)-1 && dialogueFlags[i+1] {
			paragraphs[i] = splitReSegmentQuote(paragraphs[i])
		}
	}
	var out strings.Builder
	for _, paragraph := range paragraphs {
		out.WriteByte('\n')
		out.WriteString(paragraph)
	}
	return out.String()
}

func splitReSegmentQuote(text string) string {
	runes := []rune(text)
	if len(runes) < 3 {
		return text
	}
	if matchReSegmentRune(reSegmentMarkQuotation, runes[0]) {
		idx := seekReSegmentIndex(runes, reSegmentMarkQuotation, 1, len(runes)-2, true) + 1
		if idx > 1 && idx-1 < len(runes) && !matchReSegmentRune(reSegmentMarkQuotationBefore, runes[idx-1]) {
			return string(runes[:idx]) + "\n" + string(runes[idx:])
		}
		return text
	}
	if matchReSegmentRune(reSegmentMarkQuotation, runes[len(runes)-1]) {
		idx := len(runes) - 1 - seekReSegmentIndex(runes, reSegmentMarkQuotation, 1, len(runes)-2, false)
		if idx > 1 && idx-1 < len(runes) && !matchReSegmentRune(reSegmentMarkQuotationBefore, runes[idx-1]) {
			return string(runes[:idx]) + "\n" + string(runes[idx:])
		}
	}
	return text
}

func findReSegmentNewLines(text string, dict map[string]struct{}) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}

	working := append([]rune(nil), runes...)
	quotePositions := make([]int, 0)
	insertLines := make([]int, 0)
	mod := make([]int, len(working)+2)
	waitClose := false

	for i, ch := range working {
		if !matchReSegmentRune(reSegmentMarkQuotation, ch) {
			continue
		}
		size := len(quotePositions)
		if size > 0 {
			quotePre := quotePositions[size-1]
			if i-quotePre == 2 {
				remove := false
				if waitClose {
					remove = matchReSegmentRune(",，、/", working[i-1])
				} else {
					remove = matchReSegmentRune(",，、/和与或", working[i-1])
				}
				if remove {
					working[i] = '“'
					working[i-2] = '”'
					quotePositions = quotePositions[:size-1]
					mod[size-1] = 1
					mod[size] = -1
					continue
				}
			}
		}
		quotePositions = append(quotePositions, i)
		if i <= 1 {
			continue
		}
		charB1 := working[i-1]
		var charB2 rune
		if matchReSegmentRune(reSegmentMarkQuotationBefore, charB1) {
			if len(quotePositions) > 1 {
				lastQuote := quotePositions[len(quotePositions)-2]
				p := 0
				if charB1 == ',' || charB1 == '，' {
					if len(quotePositions) > 2 {
						p = quotePositions[len(quotePositions)-3]
						if p > 0 {
							charB2 = working[p-1]
						}
					}
				}
				if matchReSegmentRune(reSegmentMarkSentencesEndP, charB2) {
					insertLines = append(insertLines, p-1)
				} else if !matchReSegmentRune("的", charB2) {
					lastEnd := seekReSegmentLast(working, reSegmentMarkSentencesEnd, i, lastQuote)
					if lastEnd > 0 {
						insertLines = append(insertLines, lastEnd)
					} else {
						insertLines = append(insertLines, lastQuote)
					}
				}
			}
			waitClose = true
			mod[size] = 1
			if size > 0 {
				mod[size-1] = -1
				if size > 1 {
					mod[size-2] = 1
				}
			}
		} else if waitClose {
			waitClose = false
			insertLines = append(insertLines, i)
		}
	}

	size := len(quotePositions)
	opend := false
	if size > 0 {
		for i := 0; i < size; i++ {
			switch {
			case mod[i] > 0:
				opend = true
			case mod[i] < 0:
				if !opend && i > 0 {
					mod[i] = 3
				}
				opend = false
			default:
				opend = !opend
				if opend {
					mod[i] = 2
				} else {
					mod[i] = -2
				}
			}
		}
		if opend {
			if quotePositions[size-1]-len(working) > -3 {
				if size > 1 {
					mod[size-2] = 4
				}
				mod[size-1] = -4
			} else if len(working) >= 2 && !matchReSegmentRune(reSegmentMarkSentencesSay, working[len(working)-2]) {
				working = append(working, '”')
			}
		}

		loop2Mod1 := -1
		i := 0
		j := quotePositions[0] - 1
		if j < 0 {
			i = 1
			loop2Mod1 = 0
		}
		for ; i < size; i++ {
			j = quotePositions[i] - 1
			loop2Mod2 := mod[i]
			if loop2Mod1 < 0 && loop2Mod2 > 0 && j >= 0 && j < len(working) && matchReSegmentRune(reSegmentMarkSentencesEnd, working[j]) {
				insertLines = append(insertLines, j)
			}
			loop2Mod1 = loop2Mod2
		}
	}

	validated := make([]int, 0, len(insertLines))
	for _, pos := range insertLines {
		if pos < 0 || pos >= len(working) {
			continue
		}
		if matchReSegmentRune(`"'”“`, working[pos]) {
			start := seekReSegmentLast(runes, `"'”“`, pos-1, pos-reSegmentWordMaxLength)
			if start > 0 && pos <= len(runes) {
				word := string(runes[start+1 : pos])
				if _, ok := dict[word]; ok {
					continue
				}
				if matchReSegmentRune("的地得", runes[start]) {
					continue
				}
			}
		}
		validated = append(validated, pos)
	}
	insertLines = uniqueSortedInts(validated)

	progress := 0
	lineIndex := 0
	nextLine := -1
	if len(insertLines) > 0 {
		nextLine = insertLines[lineIndex]
	}
	for _, quote := range quotePositions {
		gain := 4
		minCount := 2
		trigger := 4
		if quote <= 0 {
			gain = 3
			minCount = 0
			trigger = 2
		}
		for lineIndex < len(insertLines) {
			if nextLine >= quote {
				break
			}
			nextLine = insertLines[lineIndex]
			if progress < nextLine {
				part := string(working[progress:nextLine])
				insertLines = append(insertLines, forceReSegmentSplit(part, progress, minCount, gain, trigger)...)
				progress = nextLine + 1
			}
			lineIndex++
		}
		if progress <= quote {
			part := string(working[progress : quote+1])
			insertLines = append(insertLines, forceReSegmentSplit(part, progress, minCount, gain, trigger)...)
			progress = quote + 1
		}
	}
	for lineIndex < len(insertLines) {
		nextLine = insertLines[lineIndex]
		if progress <= nextLine {
			part := string(working[progress:nextLine])
			insertLines = append(insertLines, forceReSegmentSplit(part, progress, 3, 3, 2)...)
			progress = nextLine + 1
		}
		lineIndex++
	}
	if progress < len(working) {
		part := string(working[progress:])
		insertLines = append(insertLines, forceReSegmentSplit(part, progress, 3, 3, 2)...)
	}

	insertQuote := make([]bool, size)
	opend = false
	for i, quote := range quotePositions {
		switch {
		case mod[i] > 0:
			working[quote] = '“'
			if opend {
				insertQuote[i] = true
			}
			opend = true
		case mod[i] < 0:
			working[quote] = '”'
			opend = false
		default:
			opend = !opend
			if opend {
				working[quote] = '“'
			} else {
				working[quote] = '”'
			}
		}
	}

	insertLines = uniqueSortedInts(insertLines)
	output := make([]rune, 0, int(float64(len(working))*1.15))
	lineIndex = 0
	progress = 0
	nextLine = -1
	if len(insertLines) > 0 {
		nextLine = insertLines[lineIndex]
	}
	for i, quote := range quotePositions {
		for lineIndex < len(insertLines) {
			if nextLine >= quote {
				break
			}
			nextLine = insertLines[lineIndex]
			if progress < nextLine+1 {
				output = append(output, working[progress:nextLine+1]...)
				output = append(output, '\n')
				progress = nextLine + 1
			}
			lineIndex++
		}
		if progress < quote+1 {
			output = append(output, working[progress:quote+1]...)
			progress = quote + 1
		}
		if insertQuote[i] && len(output) > 2 {
			if output[len(output)-1] == '\n' {
				output = append(output, '“')
			} else {
				last := output[len(output)-1]
				output = output[:len(output)-1]
				output = append(output, '”', '\n', last)
			}
		}
	}
	for lineIndex < len(insertLines) {
		nextLine = insertLines[lineIndex]
		if progress <= nextLine && nextLine < len(working) {
			output = append(output, working[progress:nextLine+1]...)
			output = append(output, '\n')
			progress = nextLine + 1
		}
		lineIndex++
	}
	if progress < len(working) {
		output = append(output, working[progress:]...)
	}
	return string(output)
}

func makeReSegmentDict(content string) map[string]struct{} {
	matches := reSegmentDictWord.FindAllStringSubmatch(content, -1)
	cache := map[string]struct{}{}
	dict := map[string]struct{}{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		word := match[1]
		if _, ok := cache[word]; ok {
			dict[word] = struct{}{}
			continue
		}
		cache[word] = struct{}{}
	}
	return dict
}

func forceReSegmentSplit(text string, offset int, minCount int, gain int, trigger int) []int {
	runes := []rune(text)
	arrayEnd := seekReSegmentIndexes(runes, reSegmentMarkSentencesEndP, 0, len(runes)-2, true)
	arrayMid := seekReSegmentIndexes(runes, reSegmentMarkSentencesMid, 0, len(runes)-2, true)
	if len(arrayEnd) < trigger && len(arrayMid) < trigger*3 {
		return nil
	}
	result := make([]int, 0)
	j := 0
	i := minCount
	for i < len(arrayEnd) {
		k := 0
		for j < len(arrayMid) {
			if arrayMid[j] < arrayEnd[i] {
				k++
			}
			j++
		}
		if deterministicReSegmentScore(text, offset, i, k)*float64(gain) < 0.8+float64(k)/2.5 {
			result = append(result, arrayEnd[i]+offset)
			if next := maxInt(i+minCount, i); next > i {
				i = next
			}
		}
		i++
	}
	return result
}

func deterministicReSegmentScore(text string, offset int, idx int, midCount int) float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.Itoa(offset)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.Itoa(idx)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.Itoa(midCount)))
	return float64(h.Sum64()%10000) / 10000.0
}

func seekReSegmentIndexes(runes []rune, key string, from int, to int, inOrder bool) []int {
	list := make([]int, 0)
	if len(runes)-from < 1 {
		return list
	}
	i := 0
	if from > 0 {
		i = from
	}
	limit := len(runes)
	if to > 0 && to < limit {
		limit = to
	}
	for i < limit {
		var ch rune
		if inOrder {
			ch = runes[i]
		} else {
			ch = runes[len(runes)-i-1]
		}
		if matchReSegmentRune(key, ch) {
			if len(list) > 0 && i-list[len(list)-1] == 1 {
				list[len(list)-1] = i
			} else {
				list = append(list, i)
			}
		}
		i++
	}
	return list
}

func seekReSegmentLast(runes []rune, key string, from int, to int) int {
	if len(runes)-from < 1 {
		return -1
	}
	i := len(runes) - 1
	if from < i && i > 0 {
		i = from
	}
	limit := 0
	if to > 0 {
		limit = to
	}
	for i > limit {
		if matchReSegmentRune(key, runes[i]) {
			return i
		}
		i--
	}
	return -1
}

func seekReSegmentIndex(runes []rune, key string, from int, to int, inOrder bool) int {
	if len(runes)-from < 1 {
		return -1
	}
	i := 0
	if from > 0 {
		i = from
	}
	limit := len(runes)
	if to > 0 && to < limit {
		limit = to
	}
	for i < limit {
		var ch rune
		if inOrder {
			ch = runes[i]
		} else {
			ch = runes[len(runes)-i-1]
		}
		if matchReSegmentRune(key, ch) {
			return i
		}
		i++
	}
	return -1
}

func matchReSegmentRune(rule string, chr rune) bool {
	return strings.ContainsRune(rule, chr)
}

func uniqueSortedInts(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	sort.Ints(values)
	out := values[:0]
	last := values[0] - 1
	for _, value := range values {
		if value < 0 {
			continue
		}
		if len(out) == 0 || value != last {
			out = append(out, value)
			last = value
		}
	}
	return out
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
