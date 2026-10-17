package css

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

const searchHTML = `
<html><body>
  <div class="book"><a class="name" href="/b/1">第一本</a><span class="author">作者甲</span><p class="intro">简介一</p></div>
  <div class="book"><a class="name" href="/b/2">第二本</a><span class="author">作者乙</span><p class="intro">简介二</p></div>
</body></html>`

func TestAdapterExtractsSearchListFields(t *testing.T) {
	adapter, err := New(searchHTML)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []struct {
		name string
		rule string
		want []string
	}{
		{name: "names", rule: ".book@.name@text", want: []string{"第一本", "第二本"}},
		{name: "authors", rule: ".book@.author@text", want: []string{"作者甲", "作者乙"}},
		{name: "urls", rule: ".book@.name@href", want: []string{"/b/1", "/b/2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := adapter.Strings(tt.rule)
			if err != nil {
				t.Fatalf("Strings() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("values = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestAdapterSupportsHTMLAllAndOwnText(t *testing.T) {
	adapter, err := New(`<div class="x">own<span>child</span><script>bad()</script></div>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	own, err := adapter.String(".x@ownText")
	if err != nil {
		t.Fatalf("ownText error = %v", err)
	}
	if own != "own" {
		t.Fatalf("ownText = %q, want own", own)
	}
	html, err := adapter.String(".x@html")
	if err != nil {
		t.Fatalf("html error = %v", err)
	}
	if html != `<div class="x">own<span>child</span></div>` {
		t.Fatalf("html = %q", html)
	}
	all, err := adapter.String(".x@all")
	if err != nil {
		t.Fatalf("all error = %v", err)
	}
	if all == "" {
		t.Fatal("all is empty")
	}
}

func TestAdapterHTMLExtractorReturnsOuterHTMLForVoidElement(t *testing.T) {
	adapter, err := New(`<div class="infoText"><img data-src="/1.jpg" src="/thumb.jpg"></div>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.String(`.infoText@img@html`)
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	if got != `<img data-src="/1.jpg" src="/thumb.jpg"/>` && got != `<img data-src="/1.jpg" src="/thumb.jpg">` {
		t.Fatalf("got = %q", got)
	}
}

func TestAdapterSupportsTextNodesExtractor(t *testing.T) {
	adapter, err := New(`<div class="markdown-body"><p>第一段<span>忽略</span>尾注</p><p>第二段</p></div>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.String(`.markdown-body@p@textNodes`)
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	want := "第一段\n尾注\n第二段"
	if got != want {
		t.Fatalf("got = %q, want %q", got, want)
	}
}

func TestAdapterSupportsSelectorListWithInvalidFallbackBranch(t *testing.T) {
	adapter, err := New(`<div class="forum-content mt-3"><p>第一章</p></div>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.String(`@css:div.forum-content.mt-3,div[class =d_post_content j_d_post_content]@all`)
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	if got == "" || !strings.Contains(got, "第一章") {
		t.Fatalf("got = %q", got)
	}
}

func TestAdapterSupportsTrailingIndexSelectorSyntax(t *testing.T) {
	adapter, err := New(`<div><a href="/1">第一本</a><a href="/2">第二本</a><a href="/3">第三本</a></div>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	first, err := adapter.String(`a.0@text`)
	if err != nil {
		t.Fatalf("first String() error = %v", err)
	}
	if first != "第一本" {
		t.Fatalf("first = %q", first)
	}
	last, err := adapter.String(`tag.a.-1@href`)
	if err != nil {
		t.Fatalf("last String() error = %v", err)
	}
	if last != "/3" {
		t.Fatalf("last = %q", last)
	}
}

func TestAdapterSupportsLegacyExcludeIndexSyntax(t *testing.T) {
	adapter, err := New(`<div class="box"><div>头部</div><div>正文一</div><div>正文二</div><div>尾部</div></div>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.String(`.box@div!-1@html`)
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	if !strings.Contains(got, "头部") || !strings.Contains(got, "正文一") || !strings.Contains(got, "正文二") {
		t.Fatalf("got = %q", got)
	}
	if strings.Contains(got, "尾部") {
		t.Fatalf("got still contains excluded tail: %q", got)
	}
}

func TestAdapterSupportsBracketRangeAndLegacyMultiIndexSyntax(t *testing.T) {
	adapter, err := New(`<div class="infoText"><li>零</li><li>一</li><li>二</li><li>三</li><li>四</li><p>甲</p><p>乙</p><p>丙</p></div>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	rangeGot, err := adapter.String(`.infoText@li[1:3]@text`)
	if err != nil {
		t.Fatalf("range String() error = %v", err)
	}
	if rangeGot != "一\n二\n三" {
		t.Fatalf("rangeGot = %q", rangeGot)
	}
	multiGot, err := adapter.String(`.infoText@p.-2:-1@text`)
	if err != nil {
		t.Fatalf("multi String() error = %v", err)
	}
	if multiGot != "乙\n丙" {
		t.Fatalf("multiGot = %q", multiGot)
	}
}

func TestAdapterSupportsJsoupRegexAttributeSelectors(t *testing.T) {
	adapter, err := New(`<html><head>
<meta property="og:novel:category" content="诸子百家">
<meta property="og:novel:latest_chapter_name" content="最新章节">
<meta name="category" content="典藏">
</head><body>
<a href="/catalog/1">目录</a>
</body></html>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.String(`[property~=category|status|update_time]@content`)
	if err != nil {
		t.Fatalf("String(category) error = %v", err)
	}
	if got != "诸子百家" {
		t.Fatalf("category = %q", got)
	}
	latest, err := adapter.String(`[property~=las?test_chapter_name]@content`)
	if err != nil {
		t.Fatalf("String(latest) error = %v", err)
	}
	if latest != "最新章节" {
		t.Fatalf("latest = %q", latest)
	}
	nameCategory, err := adapter.String(`[name~=category|status|update_time]@content`)
	if err != nil {
		t.Fatalf("String(name category) error = %v", err)
	}
	if nameCategory != "典藏" {
		t.Fatalf("nameCategory = %q", nameCategory)
	}
	href, err := adapter.String(`a[href~=catalog]@href`)
	if err != nil {
		t.Fatalf("String(href regex) error = %v", err)
	}
	if href != "/catalog/1" {
		t.Fatalf("href = %q", href)
	}
}

func TestAdapterExtractsAttributeFromFragmentRootChild(t *testing.T) {
	adapter, err := New(`<a href="/chapter/1.html">第一章</a>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.String("href")
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	if got != "/chapter/1.html" {
		t.Fatalf("href = %q", got)
	}
}

func TestAdapterFindsSelfWhenSelectionAlreadyMatchesSelector(t *testing.T) {
	adapter, err := New(`<a class="WorkTocSection_link__ocg9K" href="/works/1/episodes/2"><div class="WorkTocSection_title__H2007">第1话</div></a>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	selection, err := adapter.Elements(`class.WorkTocSection_link__ocg9K`)
	if err != nil {
		t.Fatalf("Elements() error = %v", err)
	}
	child := NewFromSelection(selection.First())
	got, err := child.String(`tag.a@href`)
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	if got != "/works/1/episodes/2" {
		t.Fatalf("href = %q", got)
	}
}

func TestAdapterSupportsLeadingAtExtractor(t *testing.T) {
	adapter, err := New(`<a href="/chapter/1.html" onclick="openBook('/book/42', '', '')">第一章</a>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.String("@onclick")
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	if got != "openBook('/book/42', '', '')" {
		t.Fatalf("onclick = %q", got)
	}
}

func TestAdapterSupportsClassSugarWithMultipleClasses(t *testing.T) {
	adapter, err := New(`<div class="entry-content clearfix"><p>第一段</p><p>第二段</p></div>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.String("class.entry-content clearfix@html")
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	if got != `<div class="entry-content clearfix"><p>第一段</p><p>第二段</p></div>` {
		t.Fatalf("got = %q", got)
	}
}

func TestAdapterElementsAllowsSimpleTagSelectorInChain(t *testing.T) {
	adapter, err := New(`<div class="rank-book-list"><a href="/b/1">第一本</a><a href="/b/2">第二本</a></div>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.Elements(".rank-book-list@a")
	if err != nil {
		t.Fatalf("Elements() error = %v", err)
	}
	if got.Length() != 2 {
		t.Fatalf("elements length = %d", got.Length())
	}
}

func TestAdapterDoesNotFallbackAttributeForEmptySelection(t *testing.T) {
	adapter, err := New(`<html><body><a href="/home">首页</a></body></html>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.String(".next@href")
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	if got != "" {
		t.Fatalf("href = %q, want empty", got)
	}
}

func TestAdapterDoesNotDescendAttributeLookupFromMatchedHTMLFallback(t *testing.T) {
	adapter, err := New(`<html><head><link rel="stylesheet" href="/css/style.css"></head><body><h1>将进酒</h1></body></html>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.String("tag.html@href")
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	if got != "" {
		t.Fatalf("href = %q, want empty", got)
	}
}

func TestAdapterSupportsLegadoTextPrefixSelector(t *testing.T) {
	adapter, err := New(`<html><body><div><a href="/toc/1">目录</a><a href="/toc/all">查看更多章节</a></div></body></html>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.String("text.查看更多章节@href")
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	if got != "/toc/all" {
		t.Fatalf("href = %q, want /toc/all", got)
	}
}

func TestAdapterTextPrefixPrefersDeepestMatchingNode(t *testing.T) {
	adapter, err := New(`<html><body><div class="wrap"><div class="inner"><a href="/toc/all">查看更多章节</a></div></div></body></html>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := adapter.String("text.查看更多章节@href")
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	if got != "/toc/all" {
		t.Fatalf("href = %q, want /toc/all", got)
	}
}

func TestAdapterSupportsFallbackAndInterleave(t *testing.T) {
	adapter, err := New(searchHTML)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	fallback, err := adapter.Strings(".missing@text||.book@.name@text")
	if err != nil {
		t.Fatalf("fallback error = %v", err)
	}
	if !reflect.DeepEqual(fallback, []string{"第一本", "第二本"}) {
		t.Fatalf("fallback = %#v", fallback)
	}
	interleaved, err := adapter.Strings(".book@.name@text%%.book@.author@text")
	if err != nil {
		t.Fatalf("interleave error = %v", err)
	}
	want := []string{"第一本", "作者甲", "第二本", "作者乙"}
	if !reflect.DeepEqual(interleaved, want) {
		t.Fatalf("interleaved = %#v, want %#v", interleaved, want)
	}
}

func TestAdapterReturnsTypedErrorForEmptySelector(t *testing.T) {
	adapter, err := New(searchHTML)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := adapter.Strings(""); !errors.Is(err, ErrEmptySelector) {
		t.Fatalf("error = %v, want ErrEmptySelector", err)
	}
}

// R-010/R-061/R-062/R-063/R-117：选择器语义对齐 Legado AnalyzeByJSoup。
func TestAdapterLegadoSelectorSemantics(t *testing.T) {
	tests := []struct {
		name string
		html string
		rule string
		want []string
	}{
		{
			name: "text prefix with legacy dot index",
			html: `<div><p>关键词甲</p><p>其他</p><p>关键词乙</p></div>`,
			rule: "text.关键词.0@text",
			want: []string{"关键词甲"},
		},
		{
			name: "text prefix with bracket index",
			html: `<div><p>关键词甲</p><p>其他</p><p>关键词乙</p></div>`,
			rule: "text.关键词[-1]@text",
			want: []string{"关键词乙"},
		},
		{
			name: "text prefix with index in middle step",
			html: `<div><p>关键词甲<a href="/1">一</a></p><p>关键词乙<a href="/2">二</a></p></div>`,
			rule: "text.关键词.1@a@href",
			want: []string{"/2"},
		},
		{
			name: "comma selector keeps document order",
			html: `<div><b>b1</b><a>a1</a><b>b2</b></div>`,
			rule: "a,b@text",
			want: []string{"b1", "a1", "b2"},
		},
		{
			name: "comma selector trailing index applies to merged result",
			html: `<div><b>b1</b><a>a1</a><b>b2</b></div>`,
			rule: "a,b.1@text",
			want: []string{"a1"},
		},
		{
			name: "children with tag suffix and index",
			html: `<div class="box"><span>零</span><p>一</p><p>二</p></div>`,
			rule: ".box@children.div.1@text",
			want: []string{"一"},
		},
		{
			name: "children with tag suffix",
			html: `<div class="box"><span>零</span><p>一</p></div>`,
			rule: ".box@children.p@text",
			want: []string{"零", "一"},
		},
		{
			name: "attribute values are deduplicated",
			html: `<div><a href="/1">一</a><a href="/1">一again</a><a href="/2">二</a></div>`,
			rule: "a@href",
			want: []string{"/1", "/2"},
		},
		{
			name: "text values are not deduplicated",
			html: `<div><a>同</a><a>同</a></div>`,
			rule: "a@text",
			want: []string{"同", "同"},
		},
		{
			name: "zero step range keeps only first",
			html: `<div><i>0</i><i>1</i><i>2</i><i>3</i></div>`,
			rule: "i[0:2:0]@text",
			want: []string{"0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, err := New(tt.html)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			got, err := adapter.Strings(tt.rule)
			if err != nil {
				t.Fatalf("Strings(%q) error = %v", tt.rule, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Strings(%q) = %#v, want %#v", tt.rule, got, tt.want)
			}
		})
	}
}

func TestAdapterElementsTextPrefixIndexAndCommaOrder(t *testing.T) {
	adapter, err := New(`<div><b>b1</b><a>关键词a</a><b>关键词b</b></div>`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	selected, err := adapter.Elements("text.关键词.0")
	if err != nil {
		t.Fatalf("Elements() error = %v", err)
	}
	if selected.Length() != 1 || selected.Text() != "关键词a" {
		t.Fatalf("text prefix elements = %d %q", selected.Length(), selected.Text())
	}
	merged, err := adapter.Elements("a,b")
	if err != nil {
		t.Fatalf("Elements() error = %v", err)
	}
	got := []string{}
	merged.Each(func(_ int, item *goquery.Selection) { got = append(got, item.Text()) })
	if want := []string{"b1", "关键词a", "关键词b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("comma elements = %#v, want %#v", got, want)
	}
}

// `text.` 规则要取 ownText 命中的最深元素：祖先后代同时命中时不能取到祖先。
func TestSelectStepTextRuleKeepsDeepestMatch(t *testing.T) {
	doc := `<div><p>查看更多章节<a href="/all">查看更多章节</a></p></div>`
	adapter, err := New(doc)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	selected, err := adapter.Elements(`text.查看更多章节`)
	if err != nil {
		t.Fatalf("Elements() error = %v", err)
	}
	if selected.Length() != 1 {
		t.Fatalf("selected length = %d, want 1", selected.Length())
	}
	if got := goquery.NodeName(selected); got != "a" {
		t.Fatalf("selected = %q, want deepest match a", got)
	}
}

// 只有祖先命中（无更深命中后代）时仍要保留祖先。
func TestSelectStepTextRuleKeepsAncestorWithoutDeeperMatch(t *testing.T) {
	doc := `<div><p>查看更多章节<span>其它</span></p></div>`
	adapter, err := New(doc)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	selected, err := adapter.Elements(`text.查看更多章节`)
	if err != nil {
		t.Fatalf("Elements() error = %v", err)
	}
	if selected.Length() != 1 {
		t.Fatalf("selected length = %d, want 1", selected.Length())
	}
	if got := goquery.NodeName(selected); got != "p" {
		t.Fatalf("selected = %q, want ancestor p", got)
	}
}
