package css

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"

	"reader/internal/infra/rule/internal/lru"
	"reader/internal/infra/rule/parser"
	"reader/internal/infra/rule/regex"
)

var ErrEmptySelector = errors.New("css selector is empty")

type Adapter struct {
	doc *goquery.Document
}

type selectorRange struct {
	start *int
	end   *int
	step  int
}

type selectorIndexExpr struct {
	single *int
	rng    *selectorRange
}

type parsedSelector struct {
	beforeRule     string
	mode           rune
	defaultIndexes []int
	indexes        []selectorIndexExpr
	hasIndex       bool
}

type regexAttrFilter struct {
	attr    string
	pattern string
}

type regexAttrSelectors struct {
	filters   []regexAttrFilter
	broadRule string
	ok        bool
}

var regexAttrSelectorPattern = regexp.MustCompile(`\[\s*([A-Za-z_][-A-Za-z0-9_:.]*)\s*~=\s*((?:\\.|[^\]])+)\]`)

// selectorCacheSize 为选择器解析/编译缓存容量；书源规则数量有限，按规则字符串复用结果。
const selectorCacheSize = 1024

var (
	matcherCache       = lru.New[string, goquery.Matcher](selectorCacheSize)
	parsedCache        = lru.New[string, parsedSelector](selectorCacheSize)
	regexAttrCache     = lru.New[string, regexAttrSelectors](selectorCacheSize)
	matchNothing       = cascadia.Selector(func(*html.Node) bool { return false })
	matchAnyElement    = compileMatcher("*")
	selectorKeywordSet = []string{"text", "children"}
)

// compileMatcher 返回缓存的 cascadia 选择器；非法选择器与 goquery 一致视为不匹配任何节点。
func compileMatcher(selector string) goquery.Matcher {
	return matcherCache.GetOrCompute(selector, func() goquery.Matcher {
		compiled, err := cascadia.Compile(selector)
		if err != nil {
			return matchNothing
		}
		return compiled
	})
}

func emptySelection(selection *goquery.Selection) *goquery.Selection {
	return selection.FindMatcher(matchNothing)
}

func New(html string) (*Adapter, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	return &Adapter{doc: doc}, nil
}

func NewFromSelection(selection *goquery.Selection) *Adapter {
	return &Adapter{doc: &goquery.Document{Selection: selection}}
}

// NewFromDocument 复用调用方已解析的 goquery 文档，避免同一份 HTML 在一次规则链内被重复解析。
// doc 仅被读取，不会修改。
func NewFromDocument(doc *goquery.Document) *Adapter {
	return &Adapter{doc: doc}
}

func (a *Adapter) Elements(rule string) (*goquery.Selection, error) {
	return selectElements(a.doc.Selection, rule)
}

func (a *Adapter) Strings(rule string) ([]string, error) {
	return extractStrings(a.doc.Selection, rule)
}

func (a *Adapter) String(rule string) (string, error) {
	values, err := a.Strings(rule)
	if err != nil {
		return "", err
	}
	if len(values) == 0 {
		return "", nil
	}
	if len(values) == 1 {
		return values[0], nil
	}
	return strings.Join(values, "\n"), nil
}

func extractStrings(root *goquery.Selection, rule string) ([]string, error) {
	rule = trimCSSPrefix(rule)
	if rule == "" {
		return nil, ErrEmptySelector
	}
	chain, err := parser.Split(rule, "&&", "||", "%%")
	if err != nil {
		return nil, err
	}
	results := [][]string{}
	for _, step := range chain.Steps {
		values, err := extractStep(root, step.Raw)
		if err != nil {
			return nil, err
		}
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
	out := []string{}
	for _, values := range results {
		out = append(out, values...)
	}
	return out, nil
}

func extractStep(root *goquery.Selection, rule string) ([]string, error) {
	rule = trimLeadingAt(rule)
	chain, err := parser.Split(rule, "@")
	if err != nil {
		return nil, err
	}
	if len(chain.Steps) == 0 {
		return nil, ErrEmptySelector
	}
	current := root
	for _, step := range chain.Steps[:len(chain.Steps)-1] {
		selector := normalizeSelectorToken(step.Raw)
		if selector == "" {
			return nil, ErrEmptySelector
		}
		current = findSelection(current, selector)
	}
	last := normalizeSelectorToken(chain.Steps[len(chain.Steps)-1].Raw)
	if last == "" {
		return nil, ErrEmptySelector
	}
	if isExtractor(last) {
		values := extractFromSelection(current, last)
		if len(values) == 0 {
			if fallback := fallbackExtractorSelection(current); fallback != nil {
				values = extractFromSelection(fallback, last)
			}
		}
		return values, nil
	}
	selected := findSelection(current, last)
	return extractFromSelection(selected, "text"), nil
}

func selectElements(root *goquery.Selection, rule string) (*goquery.Selection, error) {
	rule = trimCSSPrefix(rule)
	if rule == "" {
		return nil, ErrEmptySelector
	}
	chain, err := parser.Split(rule, "&&", "||", "%%")
	if err != nil {
		return nil, err
	}
	var out *goquery.Selection
	for _, step := range chain.Steps {
		selected, err := selectStep(root, step.Raw)
		if err != nil {
			return nil, err
		}
		if selected.Length() > 0 {
			if out == nil {
				out = selected
			} else {
				out = out.AddSelection(selected)
			}
			if chain.Separator == "||" {
				break
			}
		}
	}
	if out == nil {
		return emptySelection(root), nil
	}
	return out, nil
}

func selectStep(root *goquery.Selection, rule string) (*goquery.Selection, error) {
	rule = trimLeadingAt(rule)
	chain, err := parser.Split(rule, "@")
	if err != nil {
		return nil, err
	}
	current := root
	for _, step := range chain.Steps {
		selector := normalizeSelectorToken(step.Raw)
		if selector == "" {
			return nil, ErrEmptySelector
		}
		current = findSelection(current, selector)
	}
	return current, nil
}

func filterDeepestTextMatch(root *goquery.Selection, needle string) *goquery.Selection {
	matchedNodes := collectTextMatches(root.Nodes, needle)
	if len(matchedNodes) == 0 {
		return emptySelection(root)
	}
	// 第一遍收集命中集合，第二遍沿命中节点的祖先链标记“有更深命中后代”的祖先：
	// `text.` 规则要的是最深命中元素，命中祖先必须被剔除而不是被保留。
	matchedSet := make(map[*html.Node]struct{}, len(matchedNodes))
	for _, node := range matchedNodes {
		matchedSet[node] = struct{}{}
	}
	hasDeeper := make(map[*html.Node]struct{}, len(matchedNodes))
	for _, node := range matchedNodes {
		for parent := node.Parent; parent != nil; parent = parent.Parent {
			if _, ok := matchedSet[parent]; ok {
				hasDeeper[parent] = struct{}{}
			}
		}
	}
	var out *goquery.Selection
	for _, node := range matchedNodes {
		if _, ok := hasDeeper[node]; ok {
			continue
		}
		sel := goquery.NewDocumentFromNode(node).Selection
		if out == nil {
			out = sel
		} else {
			out = out.AddSelection(sel)
		}
	}
	if out == nil {
		return emptySelection(root)
	}
	return out
}

// collectTextMatches 单遍 DFS 收集 ownText 命中 needle 的元素节点：
// 只递归元素子节点（与 goquery FindMatcher 一致，不包含根自身），
// 每个节点只计算一次 ownText，整体 O(节点数)，不再对全文档逐元素构造 Selection。
func collectTextMatches(nodes []*html.Node, needle string) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			if nodeOwnTextContains(child, needle) {
				out = append(out, child)
			}
			walk(child)
		}
	}
	for _, n := range nodes {
		if n.Type != html.DocumentNode {
			continue
		}
		walk(n)
	}
	return out
}

// nodeOwnTextContains 判断元素的直接子文本节点拼接后是否包含 needle，
// 语义与 ownText（goquery.Nodes[0] 的直接子文本）一致；单段时免 Join 分配。
func nodeOwnTextContains(node *html.Node, needle string) bool {
	var parts []string
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			if text := strings.TrimSpace(child.Data); text != "" {
				parts = append(parts, text)
			}
		}
	}
	if len(parts) == 0 {
		return false
	}
	if len(parts) == 1 {
		return strings.Contains(parts[0], needle)
	}
	return strings.Contains(strings.Join(parts, " "), needle)
}

func trimLeadingAt(rule string) string {
	rule = strings.TrimSpace(rule)
	for strings.HasPrefix(rule, "@") {
		rule = strings.TrimSpace(strings.TrimPrefix(rule, "@"))
	}
	return rule
}

func trimCSSPrefix(rule string) string {
	rule = strings.TrimSpace(rule)
	if strings.HasPrefix(strings.ToLower(rule), "@css:") {
		rule = strings.TrimSpace(rule[5:])
	}
	return rule
}

// selectorIndexSegmentPattern 匹配规则尾部索引分段（如 0、-1、0:2、:2、0:、!0），
// class./tag. 多段取值时保留这些分段（Legado findIndexSet 在整个 rule 上剥离索引）。
var selectorIndexSegmentPattern = regexp.MustCompile(`^!?-?\d+(?::-?\d*)?$|^!?-?\d+:$|^!?-?\d*:-?\d+$|^!:-?\d+$|^!:-?\d+:$`)

// selectorTokenWithIndex 对齐 Legado getElementsSingle 的 beforeRule.split(".")：
// 取第一段命名（如 class.item.title → "item"，后续分段被忽略），
// 段内空格保留（Legado getElementsByClass("a b") 对多 class 名生效），
// 并保留其后的索引分段，保证 normalizeSelectorToken 与 findSelection 索引解析一致。
// class 前缀（asClass=true）时空格分隔的多 class 名编译为 ".a.b"；
// tag 前缀时按 getElementsByTag 语义只取首个空格前的标签名。
func selectorTokenWithIndex(raw string, nameOffset int, asClass bool) string {
	segments := strings.Split(strings.TrimSpace(raw[nameOffset:]), ".")
	name := segments[0]
	out := name
	for _, segment := range segments[1:] {
		if !selectorIndexSegmentPattern.MatchString(segment) {
			break
		}
		out += "." + segment
	}
	if asClass {
		classes := strings.Fields(name)
		if len(classes) > 1 {
			cascadiaSelector := "." + strings.Join(classes, ".")
			if out != name {
				cascadiaSelector += strings.TrimPrefix(out, name)
			}
			return cascadiaSelector
		}
		return "." + out
	}
	if idx := strings.IndexAny(name, " \t"); idx >= 0 {
		out = name[:idx] + strings.TrimPrefix(out, name)
	}
	return out
}

func normalizeSelectorToken(raw string) string {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(strings.ToLower(raw), "class."):
		// 对齐 Legado getElementsSingle：class. 只取第一段（getElementsByClass(rules[1])）。
		return selectorTokenWithIndex(raw, 6, true)
	case strings.HasPrefix(strings.ToLower(raw), "tag."):
		// 对齐 Legado getElementsByTag(rules[1])：tag. 只取第一段。
		return selectorTokenWithIndex(raw, 4, false)
	default:
		return raw
	}
}

// findSelection 对齐 Legado ElementsSingle.getElementsSingle：先剥离尾随索引（.N/!N/[...]），
// 再按前置规则取元素，最后按索引筛选。
func findSelection(root *goquery.Selection, selector string) *goquery.Selection {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return emptySelection(root)
	}
	parsed := parseSelector(selector)
	selected := selectRule(root, parsed.beforeRule)
	if selected.Length() == 0 {
		return selected
	}
	return applySelectorIndexes(selected, parsed)
}

// selectRule 处理逗号分组：各分支独立选取（允许单个分支非法或自带索引），
// 合并去重后按文档序排列，与 jsoup select("a,b") 一致。text./children 前缀规则不按逗号拆分。
func selectRule(root *goquery.Selection, rule string) *goquery.Selection {
	rule = strings.TrimSpace(rule)
	if hasSelectorKeyword(rule) {
		return selectBase(root, rule)
	}
	parts, err := parser.Split(rule, ",")
	if err != nil || len(parts.Steps) <= 1 {
		return selectBase(root, rule)
	}
	nodes := []*html.Node{}
	for _, step := range parts.Steps {
		part := strings.TrimSpace(step.Raw)
		if part == "" {
			continue
		}
		nodes = append(nodes, findSelection(root, part).Nodes...)
	}
	return emptySelection(root).AddNodes(sortDocumentOrder(nodes)...)
}

// hasSelectorKeyword 判断规则首个 "." 分段是否为 Legado 关键字（text/children）。
func hasSelectorKeyword(rule string) bool {
	head, _, _ := strings.Cut(rule, ".")
	for _, keyword := range selectorKeywordSet {
		if strings.EqualFold(head, keyword) {
			return true
		}
	}
	return false
}

// sortDocumentOrder 去重并按文档先序排列节点；不在同一棵树上的节点保持原相对顺序追加在后。
func sortDocumentOrder(nodes []*html.Node) []*html.Node {
	if len(nodes) <= 1 {
		return nodes
	}
	pending := make(map[*html.Node]struct{}, len(nodes))
	for _, node := range nodes {
		pending[node] = struct{}{}
	}
	out := make([]*html.Node, 0, len(pending))
	top := nodes[0]
	for top.Parent != nil {
		top = top.Parent
	}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if _, ok := pending[node]; ok {
			out = append(out, node)
			delete(pending, node)
		}
		for child := node.FirstChild; child != nil && len(pending) > 0; child = child.NextSibling {
			walk(child)
		}
	}
	walk(top)
	for _, node := range nodes {
		if _, ok := pending[node]; ok {
			out = append(out, node)
			delete(pending, node)
		}
	}
	return out
}

func selectBase(root *goquery.Selection, rule string) *goquery.Selection {
	rule = strings.TrimSpace(rule)
	// 对齐 Legado：前置规则为空或首段为 children 时取直接子元素（其余分段被忽略）。
	if head, _, _ := strings.Cut(rule, "."); rule == "" || strings.EqualFold(head, "children") {
		return root.ChildrenMatcher(matchAnyElement)
	}
	if parsed := parseRegexAttrSelectors(rule); parsed.ok {
		selected := root.FindMatcher(compileMatcher(parsed.broadRule))
		return withSelfMatches(root, filterSelectionByRegexAttrs(selected, parsed.filters), parsed.broadRule)
	}
	var selected *goquery.Selection
	switch {
	case strings.HasPrefix(strings.ToLower(rule), "class."):
		// 对齐 Legado getElementsSingle：class. 只按第一段匹配（getElementsByClass(rules[1])）。
		selected = root.FindMatcher(compileMatcher(selectorTokenWithIndex(rule, 6, true)))
	case strings.HasPrefix(strings.ToLower(rule), "tag."):
		// 对齐 Legado getElementsByTag(rules[1])：tag. 只按第一段匹配。
		selected = root.FindMatcher(compileMatcher(selectorTokenWithIndex(rule, 4, false)))
	case strings.HasPrefix(strings.ToLower(rule), "id."):
		selected = root.FindMatcher(compileMatcher("#" + strings.TrimSpace(rule[3:])))
	case strings.HasPrefix(strings.ToLower(rule), "text."):
		return filterDeepestTextMatch(root, strings.TrimSpace(rule[5:]))
	default:
		selected = root.FindMatcher(compileMatcher(rule))
	}
	return withSelfMatches(root, selected, rule)
}

func withSelfMatches(root *goquery.Selection, selected *goquery.Selection, rule string) *goquery.Selection {
	if selected != nil && selected.Length() > 0 {
		return selected
	}
	self := root.FilterMatcher(compileMatcher(rule))
	if self.Length() > 0 {
		return self
	}
	return selected
}

func applySelectorIndexes(selection *goquery.Selection, parsed parsedSelector) *goquery.Selection {
	if !parsed.hasIndex || parsed.mode == ' ' {
		return selection
	}
	indexes := expandSelectorIndexes(parsed, selection.Length())
	if len(indexes) == 0 {
		return emptySelection(selection)
	}
	if parsed.mode == '!' {
		excluded := map[int]struct{}{}
		for _, idx := range indexes {
			excluded[idx] = struct{}{}
		}
		var out *goquery.Selection
		selection.Each(func(i int, item *goquery.Selection) {
			if _, ok := excluded[i]; ok {
				return
			}
			if out == nil {
				out = item
			} else {
				out = out.AddSelection(item)
			}
		})
		if out == nil {
			return emptySelection(selection)
		}
		return out
	}
	var out *goquery.Selection
	for _, idx := range indexes {
		item := selection.Eq(idx)
		if out == nil {
			out = item
		} else {
			out = out.AddSelection(item)
		}
	}
	if out == nil {
		return emptySelection(selection)
	}
	return out
}

func expandSelectorIndexes(parsed parsedSelector, length int) []int {
	if length <= 0 {
		return nil
	}
	ordered := []int{}
	seen := map[int]struct{}{}
	appendIndex := func(idx int) {
		if idx < 0 {
			idx += length
		}
		if idx < 0 || idx >= length {
			return
		}
		if _, ok := seen[idx]; ok {
			return
		}
		seen[idx] = struct{}{}
		ordered = append(ordered, idx)
	}
	if len(parsed.indexes) == 0 {
		for i := len(parsed.defaultIndexes) - 1; i >= 0; i-- {
			appendIndex(parsed.defaultIndexes[i])
		}
		return ordered
	}
	for i := len(parsed.indexes) - 1; i >= 0; i-- {
		expr := parsed.indexes[i]
		if expr.single != nil {
			appendIndex(*expr.single)
			continue
		}
		if expr.rng == nil {
			continue
		}
		start := 0
		if expr.rng.start != nil {
			start = *expr.rng.start
		}
		if start < 0 {
			start += length
		}
		end := length - 1
		if expr.rng.end != nil {
			end = *expr.rng.end
		}
		if end < 0 {
			end += length
		}
		if (start < 0 && end < 0) || (start >= length && end >= length) {
			continue
		}
		if start >= length {
			start = length - 1
		} else if start < 0 {
			start = 0
		}
		if end >= length {
			end = length - 1
		} else if end < 0 {
			end = 0
		}
		stepX := expr.rng.step
		if start == end || stepX >= length {
			appendIndex(start)
			continue
		}
		step := 1
		if stepX > 0 {
			step = stepX
		} else if -stepX < length {
			step = stepX + length
		}
		if end > start {
			for idx := start; idx <= end; idx += step {
				appendIndex(idx)
			}
		} else {
			for idx := start; idx >= end; idx -= step {
				appendIndex(idx)
			}
		}
	}
	return ordered
}

// parseSelector 解析规则尾随索引；结果只读，按规则字符串缓存复用。
func parseSelector(rule string) parsedSelector {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return parsedSelector{beforeRule: rule, mode: ' '}
	}
	return parsedCache.GetOrCompute(rule, func() parsedSelector {
		if rule[len(rule)-1] == ']' {
			if out, ok := parseBracketSelector(rule); ok {
				return out
			}
		}
		if out, ok := parseLegacySelector(rule); ok {
			return out
		}
		return parsedSelector{beforeRule: rule, mode: ' '}
	})
}

// parseRegexAttrSelectors 解析 jsoup 的 [attr~=regex] 选择器，结果按规则字符串缓存。
func parseRegexAttrSelectors(rule string) regexAttrSelectors {
	return regexAttrCache.GetOrCompute(rule, func() regexAttrSelectors {
		if !regexAttrSelectorPattern.MatchString(rule) {
			return regexAttrSelectors{broadRule: rule}
		}
		filters := []regexAttrFilter{}
		replaced := regexAttrSelectorPattern.ReplaceAllStringFunc(rule, func(token string) string {
			match := regexAttrSelectorPattern.FindStringSubmatch(token)
			if len(match) != 3 {
				return token
			}
			attr := strings.TrimSpace(match[1])
			pattern := strings.TrimSpace(match[2])
			pattern = strings.Trim(pattern, `"'`)
			filters = append(filters, regexAttrFilter{attr: attr, pattern: pattern})
			return "[" + attr + "]"
		})
		return regexAttrSelectors{filters: filters, broadRule: replaced, ok: true}
	})
}

func filterSelectionByRegexAttrs(selection *goquery.Selection, filters []regexAttrFilter) *goquery.Selection {
	if selection == nil || len(filters) == 0 {
		return selection
	}
	var out *goquery.Selection
	selection.Each(func(_ int, item *goquery.Selection) {
		for _, filter := range filters {
			value, ok := item.Attr(filter.attr)
			if !ok {
				return
			}
			matched, err := regex.MatchString(filter.pattern, value)
			if err != nil || !matched {
				return
			}
		}
		if out == nil {
			out = item
		} else {
			out = out.AddSelection(item)
		}
	})
	if out == nil {
		return emptySelection(selection)
	}
	return out
}

func parseBracketSelector(rule string) (parsedSelector, bool) {
	parsed := parsedSelector{beforeRule: rule, mode: ' '}
	rus := strings.TrimSpace(rule)
	length := len(rus) - 1
	digits := ""
	curMinus := false
	curRange := []*int{}
	for length--; length >= 0; length-- {
		ch := rus[length]
		if ch == ' ' {
			continue
		}
		if ch >= '0' && ch <= '9' {
			digits = string(ch) + digits
			continue
		}
		if ch == '-' {
			curMinus = true
			continue
		}
		curInt := parseOptionalIndex(digits, curMinus)
		switch ch {
		case ':':
			curRange = append(curRange, curInt)
		default:
			if len(curRange) == 0 {
				if curInt == nil {
					return parsedSelector{beforeRule: rule, mode: ' '}, false
				}
				value := *curInt
				parsed.indexes = append(parsed.indexes, selectorIndexExpr{single: &value})
			} else {
				step := 1
				if len(curRange) == 2 && curRange[0] != nil {
					step = *curRange[0]
				}
				parsed.indexes = append(parsed.indexes, selectorIndexExpr{rng: &selectorRange{
					start: curInt,
					end:   curRange[len(curRange)-1],
					step:  step,
				}})
				curRange = nil
			}
			if ch == '!' {
				parsed.mode = '!'
				for length > 0 {
					length--
					if rus[length] != ' ' {
						ch = rus[length]
						break
					}
				}
			} else {
				parsed.mode = '.'
			}
			if ch == '[' {
				parsed.beforeRule = strings.TrimSpace(rus[:length])
				parsed.hasIndex = true
				return parsed, true
			}
			if ch != ',' {
				return parsedSelector{beforeRule: rule, mode: ' '}, false
			}
		}
		digits = ""
		curMinus = false
	}
	return parsedSelector{beforeRule: rule, mode: ' '}, false
}

func parseLegacySelector(rule string) (parsedSelector, bool) {
	parsed := parsedSelector{beforeRule: rule, mode: ' '}
	rus := strings.TrimSpace(rule)
	digits := ""
	curMinus := false
	for length := len(rus) - 1; length >= 0; length-- {
		ch := rus[length]
		if ch == ' ' {
			continue
		}
		if ch >= '0' && ch <= '9' {
			digits = string(ch) + digits
			continue
		}
		if ch == '-' {
			curMinus = true
			continue
		}
		if ch != '!' && ch != '.' && ch != ':' {
			return parsedSelector{beforeRule: rule, mode: ' '}, false
		}
		if digits == "" {
			return parsedSelector{beforeRule: rule, mode: ' '}, false
		}
		value := parseOptionalIndex(digits, curMinus)
		if value == nil {
			return parsedSelector{beforeRule: rule, mode: ' '}, false
		}
		parsed.defaultIndexes = append(parsed.defaultIndexes, *value)
		if ch != ':' {
			parsed.mode = rune(ch)
			parsed.beforeRule = strings.TrimSpace(rus[:length])
			parsed.hasIndex = true
			return parsed, true
		}
		digits = ""
		curMinus = false
	}
	return parsedSelector{beforeRule: rule, mode: ' '}, false
}

func parseOptionalIndex(digits string, negative bool) *int {
	if digits == "" {
		return nil
	}
	value, err := strconv.Atoi(digits)
	if err != nil {
		return nil
	}
	if negative {
		value = -value
	}
	return &value
}

func extractFromSelection(selection *goquery.Selection, extractor string) []string {
	out := []string{}
	seenAttr := map[string]struct{}{}
	selection.Each(func(_ int, item *goquery.Selection) {
		var value string
		switch extractor {
		case "text":
			value = strings.TrimSpace(item.Text())
		case "textNodes":
			nodes := []string{}
			for _, node := range item.Nodes {
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					if child.Type != html.TextNode {
						continue
					}
					text := strings.TrimSpace(child.Data)
					if text != "" {
						nodes = append(nodes, text)
					}
				}
			}
			value = strings.Join(nodes, "\n")
		case "ownText":
			value = strings.TrimSpace(ownText(item))
		case "html":
			clone := item.Clone()
			clone.FindMatcher(compileMatcher("script,style")).Remove()
			value, _ = goquery.OuterHtml(clone)
			value = strings.TrimSpace(value)
		case "all":
			value, _ = goquery.OuterHtml(item)
			value = strings.TrimSpace(value)
		default:
			value, _ = item.Attr(extractor)
			value = strings.TrimSpace(value)
			// 对齐 Legado getResultLast：属性值重复时只保留首次出现的一条。
			if _, ok := seenAttr[value]; ok {
				return
			}
			seenAttr[value] = struct{}{}
		}
		if value != "" {
			out = append(out, value)
		}
	})
	return out
}

func ownText(selection *goquery.Selection) string {
	if len(selection.Nodes) == 0 {
		return ""
	}
	parts := []string{}
	for child := selection.Nodes[0].FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			text := strings.TrimSpace(child.Data)
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, " ")
}

func fallbackExtractorSelection(selection *goquery.Selection) *goquery.Selection {
	if selection == nil || len(selection.Nodes) == 0 || selection.Length() != 1 {
		return nil
	}
	if selection.Nodes[0].Type != html.DocumentNode {
		return nil
	}
	body := selection.FindMatcher(compileMatcher("body")).First()
	if body.Length() == 1 {
		children := body.ChildrenMatcher(matchAnyElement)
		if children.Length() == 1 {
			return children.First()
		}
	}
	children := selection.ChildrenMatcher(matchAnyElement)
	if children.Length() == 1 {
		return children.First()
	}
	return nil
}

func isExtractor(rule string) bool {
	switch rule {
	case "text", "textNodes", "ownText", "html", "all":
		return true
	default:
		return !strings.ContainsAny(rule, " >+~#.[():")
	}
}

func interleave(results [][]string) []string {
	if len(results) == 0 {
		return nil
	}
	out := []string{}
	for i := range results[0] {
		for _, values := range results {
			if i < len(values) {
				out = append(out, values[i])
			}
		}
	}
	return out
}
