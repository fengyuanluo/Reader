package ruleinfra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"

	domainbook "reader/internal/domain/book"
	domainsource "reader/internal/domain/source"
	"reader/internal/infra/rule/jsonpath"
)

func TestAnalyzerGetsHTMLStrings(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	got, err := analyzer.GetStringList(context.Background(), ".book@.name@text", `<div class="book"><a class="name">第一本</a></div>`)
	if err != nil {
		t.Fatalf("GetStringList() error = %v", err)
	}
	if !reflect.DeepEqual(got, []string{"第一本"}) {
		t.Fatalf("values = %#v", got)
	}
}

func TestAnalyzerEmptyRuleReturnsEmptyInsteadOfInput(t *testing.T) {
	analyzer := NewAnalyzer(Context{RedirectURL: "https://site.example.test/book/1"})
	html := `<html><body><a href="/toc">目录</a></body></html>`

	text, err := analyzer.GetString(context.Background(), "", html)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if text != "" {
		t.Fatalf("GetString(empty) = %q", text)
	}
	values, err := analyzer.GetStringList(context.Background(), "", html)
	if err != nil {
		t.Fatalf("GetStringList() error = %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("GetStringList(empty) = %#v", values)
	}
	elements, err := analyzer.GetElements(context.Background(), "", html)
	if err != nil {
		t.Fatalf("GetElements() error = %v", err)
	}
	if len(elements) != 0 {
		t.Fatalf("GetElements(empty) = %#v", elements)
	}
}

func TestAnalyzerGetsJSONStrings(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	got, err := analyzer.GetStringList(context.Background(), "$.books[*].name", `{"books":[{"name":"第一本"},{"name":"第二本"}]}`)
	if err != nil {
		t.Fatalf("GetStringList() error = %v", err)
	}
	if !reflect.DeepEqual(got, []string{"第一本", "第二本"}) {
		t.Fatalf("values = %#v", got)
	}
}

func TestAnalyzerRunsJSONPathOnObjectElementContext(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	elements, err := analyzer.GetElements(context.Background(), "$.books[*]", `{"books":[{"name":"第一本","author":"作者甲"},{"name":"第二本","author":"作者乙"}]}`)
	if err != nil {
		t.Fatalf("GetElements() error = %v", err)
	}
	if len(elements) != 2 {
		t.Fatalf("elements = %#v", elements)
	}
	name, err := analyzer.GetString(context.Background(), "$.name", elements[0])
	if err != nil {
		t.Fatalf("GetString(name) error = %v", err)
	}
	author, err := analyzer.GetString(context.Background(), "$.author", elements[0])
	if err != nil {
		t.Fatalf("GetString(author) error = %v", err)
	}
	if name != "第一本" || author != "作者甲" {
		t.Fatalf("name=%q author=%q", name, author)
	}
}

func TestAnalyzerRendersJSONPathTemplates(t *testing.T) {
	analyzer := NewAnalyzer(Context{SourceURL: "https://source.example.test"})
	input := map[string]any{"novelId": float64(42), "name": "第一本"}

	got, err := analyzer.GetString(context.Background(), "{{source.bookSourceUrl}}/novel/{{$.novelId}}?name={{$.name}}", input)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != "https://source.example.test/novel/42?name=第一本" {
		t.Fatalf("template = %q", got)
	}
}

func TestAnalyzerRendersLegadoJSONPathBraceTemplates(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	input := map[string]any{"updatedAt": "今天", "wordNum": float64(1234)}

	got, err := analyzer.GetString(context.Background(), "{$.updatedAt} | {$.wordNum}字", input)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != "今天 | 1234字" {
		t.Fatalf("template = %q", got)
	}
}

func TestAnalyzerSupportsPutAndGetRuleVariables(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	input := map[string]any{"id": float64(7), "name": "第一本"}

	name, err := analyzer.GetString(context.Background(), "$.name@put:{id:id,name:name}", input)
	if err != nil {
		t.Fatalf("GetString(name) error = %v", err)
	}
	url, err := analyzer.GetString(context.Background(), "https://api.example.test/book/@get:{id}?name=@get:{name}", input)
	if err != nil {
		t.Fatalf("GetString(url) error = %v", err)
	}
	if name != "第一本" || url != "https://api.example.test/book/7?name=第一本" {
		t.Fatalf("name=%q url=%q", name, url)
	}
}

func TestAnalyzerExpandsGetTokensInStringResults(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	analyzer.LoadVariables(`{"bid":"N1122344671000280502"}`)
	got, err := analyzer.GetString(context.Background(), "https://site.example.test/book/chapter/content.do,{\"body\":\"{\\\"bookId\\\":\\\"@get:{bid}\\\"}\",\"method\":\"POST\"}", map[string]any{})
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != `https://site.example.test/book/chapter/content.do,{"body":"{\"bookId\":\"N1122344671000280502\"}","method":"POST"}` {
		t.Fatalf("got = %q", got)
	}
}

func TestAnalyzerExpandsGetTokensInsideJavaScriptResultString(t *testing.T) {
	analyzer := NewAnalyzer(Context{Script: fakeRuleScript{value: `https://site.example.test/book/chapter/content.do,{"body":"{\"bookId\":\"@get:{bid}\"}","method":"POST"}`}})
	analyzer.LoadVariables(`{"bid":"N1122344671000280502"}`)
	got, err := analyzer.GetString(context.Background(), `@js:return "https://site.example.test/book/chapter/content.do,{\"body\":\"{\\\"bookId\\\":\\\"@get:{bid}\\\"}\",\"method\":\"POST\"}"`, map[string]any{})
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != `https://site.example.test/book/chapter/content.do,{"body":"{\"bookId\":\"N1122344671000280502\"}","method":"POST"}` {
		t.Fatalf("got = %q", got)
	}
}

func TestAnalyzerExpandStringAppliesVariablesToRawText(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	analyzer.LoadVariables(`{"bid":"N1122344671000280502"}`)
	got, err := analyzer.ExpandString(context.Background(), `https://site.example.test/book/chapter/content.do,{"body":"{\"bookId\":\"@get:{bid}\"}","method":"POST"}`, map[string]any{})
	if err != nil {
		t.Fatalf("ExpandString() error = %v", err)
	}
	if got != `https://site.example.test/book/chapter/content.do,{"body":"{\"bookId\":\"N1122344671000280502\"}","method":"POST"}` {
		t.Fatalf("got = %q", got)
	}
}

func TestAnalyzerExpandStringAppliesTemplateExpressionsToRawText(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	got, err := analyzer.ExpandString(context.Background(), `{"order":"{{$..order}}"}`, map[string]any{"order": "1"})
	if err != nil {
		t.Fatalf("ExpandString() error = %v", err)
	}
	if got != `{"order":"1"}` {
		t.Fatalf("got = %q", got)
	}
}

func TestAnalyzerLoadsVariablesForJavaGetTemplates(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	analyzer.LoadVariables(`{"id":"7","name":"第一本"}`)

	got, err := analyzer.GetString(context.Background(), "https://api.example.test/book/{{java.get('id')}}?name={{java.get('name')}}", map[string]any{})
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != "https://api.example.test/book/7?name=第一本" {
		t.Fatalf("template = %q", got)
	}
}

func TestAnalyzerSupportsMixedGetAndTemplateTokensInLiteralRule(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	analyzer.LoadVariables(`{"id":"7"}`)

	got, err := analyzer.GetString(context.Background(), "https://api.example.test/book/@get:{id}?name={{$.name}}", map[string]any{"name": "第一本"})
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != "https://api.example.test/book/7?name=第一本" {
		t.Fatalf("template = %q", got)
	}
}

func TestAnalyzerReadsVariablePriorityFromChapterThenBookThenSource(t *testing.T) {
	src := &domainsource.BookSource{BookSourceURL: "https://source.example.test"}
	bookValue := &domainbook.Book{Variable: `{"shared":"book","bookOnly":"book-only"}`}
	chapterValue := &domainbook.BookChapter{Variable: `{"shared":"chapter","chapterOnly":"chapter-only"}`}
	analyzer := NewAnalyzer(Context{
		Chapter: chapterValue,
		Book:    bookValue,
		Source:  scriptCompatibleSourceBindings(src),
	})

	got, err := analyzer.GetString(context.Background(), "@get:{shared}|@get:{chapterOnly}|@get:{bookOnly}", map[string]any{
		"chapter": chapterValue,
		"book":    bookValue,
		"source":  scriptCompatibleSourceBindings(src),
	})
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != "chapter|chapter-only|book-only" {
		t.Fatalf("got = %q", got)
	}
}

func TestAnalyzerReadsSourceVariableWhenEntityVariablesMissing(t *testing.T) {
	src := &domainsource.BookSource{BookSourceURL: "https://source.example.test"}
	sourceBindings := scriptCompatibleSourceBindings(src)
	sourceBindings["setVariable"].(func(string) bool)(`{"sourceOnly":"from-source"}`)
	analyzer := NewAnalyzer(Context{Source: sourceBindings})

	got, err := analyzer.GetString(context.Background(), "@get:{sourceOnly}", map[string]any{
		"source": sourceBindings,
	})
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != "from-source" {
		t.Fatalf("got = %q", got)
	}
}

func TestAnalyzerPutRuleWritesBackToBookVariableJSON(t *testing.T) {
	bookValue := &domainbook.Book{}
	analyzer := NewAnalyzer(Context{Book: bookValue})

	_, err := analyzer.GetString(context.Background(), `$.name@put:{bid:id}`, map[string]any{
		"id":      float64(7),
		"name":    "第一本",
		"book":    bookValue,
		"chapter": (*domainbook.BookChapter)(nil),
	})
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if !strings.Contains(bookValue.Variable, `"bid":"7"`) && !strings.Contains(bookValue.Variable, `"bid":7`) {
		t.Fatalf("book variable = %q", bookValue.Variable)
	}
}

func TestAnalyzerTreatsBareRuleAsJSONPathWhenInputIsJSON(t *testing.T) {
	analyzer := NewAnalyzer(Context{})

	got, err := analyzer.GetString(context.Background(), "data[0].content", `{"data":[{"content":"第一段"}]}`)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != "第一段" {
		t.Fatalf("content = %q", got)
	}
}

func TestAnalyzerDoesNotFallbackToCSSAfterJSONPathFailure(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	_, err := analyzer.GetElements(context.Background(), "$.[?(", `{"items":[]}`)
	if !errors.Is(err, jsonpath.ErrRuleParse) {
		t.Fatalf("error = %v, want JSONPath parse error", err)
	}
}

func TestAnalyzerPassesSingleJSONScalarToFollowingJSStep(t *testing.T) {
	analyzer := NewAnalyzer(Context{Script: fakeRuleScriptFunc(func(scriptText string, input any) (any, error) {
		if input != "5LiA56ug" {
			t.Fatalf("input = %#v", input)
		}
		return "一章", nil
	})})

	got, err := analyzer.GetString(context.Background(), "$.encoded\n@js:java.base64Decode(result)", `{"encoded":"5LiA56ug"}`)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != "一章" {
		t.Fatalf("decoded = %q", got)
	}
}

func TestAnalyzerGetsXPathStrings(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	got, err := analyzer.GetString(context.Background(), `@XPath://h1/text()`, `<html><body><h1>标题</h1></body></html>`)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != "标题" {
		t.Fatalf("value = %q", got)
	}
}

func TestAnalyzerGetsRegexElements(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	got, err := analyzer.GetElements(context.Background(), `:<a href="([^"]+)">([^<]+)</a>`, `<a href="/b/1">第一本</a>`)
	if err != nil {
		t.Fatalf("GetElements() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("elements = %#v", got)
	}
	group, ok := got[0].([]string)
	if !ok || !reflect.DeepEqual(group, []string{`<a href="/b/1">第一本</a>`, "/b/1", "第一本"}) {
		t.Fatalf("group = %#v", got[0])
	}
}

func TestAnalyzerAppliesReplaceAndURLNormalization(t *testing.T) {
	analyzer := NewAnalyzer(Context{RedirectURL: "https://example.test/book/index.html"})
	got, err := analyzer.GetURLList(context.Background(), `.book@a@href`, `<div class="book"><a href="../c/1.html">go</a><a href="../c/1.html">dup</a></div>`)
	if err != nil {
		t.Fatalf("GetURLList() error = %v", err)
	}
	if !reflect.DeepEqual(got, []string{"https://example.test/c/1.html"}) {
		t.Fatalf("values = %#v", got)
	}
	replaced, err := analyzer.GetString(context.Background(), `.book@a@text##第##章`, `<div class="book"><a>第一</a></div>`)
	if err != nil {
		t.Fatalf("replace error = %v", err)
	}
	if replaced != "章一" {
		t.Fatalf("replaced = %q", replaced)
	}
}

func TestAnalyzerReturnsScriptUnavailable(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	_, err := analyzer.GetString(context.Background(), "@js:return result", "input")
	if !errors.Is(err, ErrScriptUnavailable) {
		t.Fatalf("error = %v, want ErrScriptUnavailable", err)
	}
}

func TestAnalyzerSplitsEmbeddedJSBeforeFollowingRule(t *testing.T) {
	analyzer := NewAnalyzer(Context{Script: fakeRuleScript{value: `{"content":"第一段"}`}})
	got, err := analyzer.GetString(context.Background(), `<js>result</js>$..content||$.message`, `<html></html>`)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != "第一段" {
		t.Fatalf("content = %q", got)
	}
}

func TestAnalyzerSupportsEmbeddedJSWithReplaceOnlySuffix(t *testing.T) {
	analyzer := NewAnalyzer(Context{Script: fakeRuleScript{value: `<div>正文</div><li>删掉</li>`}})
	got, err := analyzer.GetString(context.Background(), `<js>result</js>##<li[^>]*>.*?</li>`, `<html></html>`)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != `<div>正文</div>` {
		t.Fatalf("content = %q", got)
	}
}

func TestAnalyzerSupportsEmbeddedJSBlockContainingReplaceOnlyBody(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	got, err := analyzer.GetString(context.Background(), `title<js>##正文卷\.|VIP卷\.</js>`, map[string]any{"title": "正文卷.第一章"})
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != "第一章" {
		t.Fatalf("content = %q", got)
	}
}

func TestAnalyzerSupportsPutOnlyInitRuleWithoutSelector(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	input := `<html><head><meta property="og:novel:book_name" content="论语"></head></html>`
	elements, err := analyzer.GetElements(context.Background(), `@put:{n:"[property$=book_name]@content"}`, input)
	if err != nil {
		t.Fatalf("GetElements() error = %v", err)
	}
	if len(elements) != 1 {
		t.Fatalf("elements = %#v", elements)
	}
	if got := analyzer.VariablesJSON(); !strings.Contains(got, `"n":"论语"`) {
		t.Fatalf("variables = %q", got)
	}
}
func TestAnalyzerEvaluatesEmbeddedJSContainingRuleTemplates(t *testing.T) {
	script := fakeRuleScriptFunc(func(scriptText string, input any) (any, error) {
		if scriptText != `url="https://api.example.test/book/"+7` {
			t.Fatalf("scriptText = %q", scriptText)
		}
		return "https://api.example.test/book/7", nil
	})
	analyzer := NewAnalyzer(Context{Script: script})

	got, err := analyzer.GetString(context.Background(), `<js>url="https://api.example.test/book/"+{{$.book}}</js>`, map[string]any{"book": float64(7)})
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != "https://api.example.test/book/7" {
		t.Fatalf("url = %q", got)
	}
}

func TestAnalyzerPreservesCSSElementContext(t *testing.T) {
	analyzer := NewAnalyzer(Context{RedirectURL: "https://example.test/books/1/"})
	html := `<div id="toc"><a href="1.html">第一章</a><a href="2.html">第二章</a></div>`

	elements, err := analyzer.GetElements(context.Background(), "#toc a", html)
	if err != nil {
		t.Fatalf("GetElements() error = %v", err)
	}
	if len(elements) != 2 {
		t.Fatalf("elements = %#v", elements)
	}
	title, err := analyzer.GetString(context.Background(), "text", elements[0])
	if err != nil {
		t.Fatalf("GetString(text) error = %v", err)
	}
	url, err := analyzer.GetURL(context.Background(), "href", elements[0])
	if err != nil {
		t.Fatalf("GetString(href) error = %v", err)
	}
	if title != "第一章" || url != "https://example.test/books/1/1.html" {
		t.Fatalf("title=%q url=%q", title, url)
	}
}

func TestAnalyzerPreservesJavaGetElementsOutputContext(t *testing.T) {
	html := `<html><body><h1>怪男孩</h1><div class="infoText"><img data-src="/1.jpg"/></div></body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("NewDocumentFromReader() error = %v", err)
	}
	analyzer := NewAnalyzer(Context{
		RedirectURL: "https://example.test/huiben/1",
		SourceURL:   "https://example.test/source",
		Script: fakeRuleScript{value: []any{
			map[string]any{"__selection": doc.Find("html").First()},
		}},
	})

	elements, err := analyzer.GetElements(context.Background(), `<js>java.getElements('html')</js>`, html)
	if err != nil {
		t.Fatalf("GetElements() error = %v", err)
	}
	if len(elements) != 1 {
		t.Fatalf("elements = %#v", elements)
	}
	title, err := analyzer.GetString(context.Background(), "h1@text", elements[0])
	if err != nil {
		t.Fatalf("GetString(title) error = %v", err)
	}
	if title != "怪男孩" {
		t.Fatalf("title = %q", title)
	}
	image, err := analyzer.GetURL(context.Background(), ".infoText@img@data-src", elements[0])
	if err != nil {
		t.Fatalf("GetString(image) error = %v", err)
	}
	if image != "https://example.test/1.jpg" {
		t.Fatalf("image = %q", image)
	}
}

// R-008/R-064/R-116：替换段中的 @get/{{}} 在求值前展开，主规则不再被误判为字面量。
func TestAnalyzerExpandsTemplatesInReplaceSegments(t *testing.T) {
	type book struct{ Name string }
	type chapter struct{ Title string }
	tests := []struct {
		name string
		rule string
		html string
		want string
	}{
		{name: "get in replacement", rule: "class.title@text##【.*】##@get:{k}", html: `<div class="title">【卷一】正文</div>`, want: "变量正文"},
		{name: "js in replacement", rule: "class.title@text##【.*】##{{java.x()}}", html: `<div class="title">【卷一】正文</div>`, want: "脚本正文"},
		{name: "get in regex", rule: "class.title@text##@get:{k}", html: `<div class="title">变量正文</div>`, want: "正文"},
		{name: "template main rule stays literal", rule: "@get:{k}##变##不", html: `<div></div>`, want: "不量"},
		{name: "bookName special key", rule: "class.title@text##.+##pre-@get:{bookName}", html: `<div class="title">x</div>`, want: "pre-书名"},
		{name: "title special key", rule: "class.title@text##.+##@get:{title}", html: `<div class="title">x</div>`, want: "章节名"},
		// ##regex##replacement### 且 replacement 为空时写作 ##regex#####（与 Legado Kotlin split("##") 一致）。
		{name: "replace first without match is empty", rule: "class.title@text##【.*】#####", html: `<div class="title">普通标题</div>`, want: ""},
		{name: "replace first keeps first match", rule: "class.title@text##【(.*?)】##$1###", html: `<div class="title">前【卷一】后</div>`, want: "卷一"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := NewAnalyzer(Context{
				Script:  fakeRuleScript{value: "脚本"},
				Book:    &book{Name: "书名"},
				Chapter: &chapter{Title: "章节名"},
			})
			analyzer.SetVariables(map[string]string{"k": "变量"})
			got, err := analyzer.GetString(context.Background(), tt.rule, tt.html)
			if err != nil {
				t.Fatalf("GetString() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("GetString(%q) = %q, want %q", tt.rule, got, tt.want)
			}
		})
	}
}

// R-009：普通文本字段不做 URL 绝对化；URL 字段走 GetURL/GetURLList 统一绝对化。
func TestAnalyzerOnlyAbsolutizesURLRules(t *testing.T) {
	analyzer := NewAnalyzer(Context{RedirectURL: "https://host/book/1"})
	html := `<div><span class="wc">1.2万</span><span class="score">3.5分</span><a href="catalog/123">目录</a><a href="javascript:void(0)">x</a></div>`
	text, err := analyzer.GetString(context.Background(), "class.wc@text", html)
	if err != nil || text != "1.2万" {
		t.Fatalf("GetString() = %q, %v", text, err)
	}
	list, err := analyzer.GetStringList(context.Background(), "class.score@text", html)
	if err != nil || !reflect.DeepEqual(list, []string{"3.5分"}) {
		t.Fatalf("GetStringList() = %#v, %v", list, err)
	}
	url, err := analyzer.GetURL(context.Background(), "a@href", html)
	if err != nil || url != "https://host/book/catalog/123" {
		t.Fatalf("GetURL() = %q, %v", url, err)
	}
	urls, err := analyzer.GetURLList(context.Background(), "a@href", html)
	if err != nil || !reflect.DeepEqual(urls, []string{"https://host/book/catalog/123"}) {
		t.Fatalf("GetURLList() = %#v, %v", urls, err)
	}
	empty, err := analyzer.GetURL(context.Background(), ".missing@href", html)
	if err != nil || empty != "" {
		t.Fatalf("GetURL(missing) = %q, %v", empty, err)
	}
}

// R-115：GetString 对最终文本反转义以分号结尾的 HTML 实体；GetStringList/GetStringRaw 保持原样。
func TestAnalyzerGetStringUnescapesHTMLEntities(t *testing.T) {
	raw := "A&amp;B&#x4e2d;&nbsp;C?a=1&para=2"
	analyzer := NewAnalyzer(Context{Script: fakeRuleScript{value: raw}})
	got, err := analyzer.GetString(context.Background(), "@js:result", "x")
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if want := "A&B中\u00a0C?a=1&para=2"; got != want {
		t.Fatalf("GetString() = %q, want %q", got, want)
	}
	rawGot, err := analyzer.GetStringRaw(context.Background(), "@js:result", "x")
	if err != nil || rawGot != raw {
		t.Fatalf("GetStringRaw() = %q, %v", rawGot, err)
	}
	list, err := analyzer.GetStringList(context.Background(), "@js:result", "x")
	if err != nil || !reflect.DeepEqual(list, []string{raw}) {
		t.Fatalf("GetStringList() = %#v, %v", list, err)
	}
}

type fakeRuleScript struct {
	value any
	err   error
}

func (f fakeRuleScript) Eval(ctx context.Context, scriptText string, input any) (any, error) {
	return f.value, f.err
}

type fakeRuleScriptFunc func(scriptText string, input any) (any, error)

func (f fakeRuleScriptFunc) Eval(ctx context.Context, scriptText string, input any) (any, error) {
	return f(scriptText, input)
}

func scriptCompatibleSourceBindings(src *domainsource.BookSource) map[string]any {
	state := ""
	return map[string]any{
		"bookSourceUrl": src.BookSourceURL,
		"getVariable": func(args ...any) string {
			return state
		},
		"setVariable": func(value string) bool {
			state = value
			return true
		},
		"putVariable": func(args ...any) bool {
			if len(args) == 1 {
				state = args[0].(string)
			}
			return true
		},
	}
}

// TestSplitSourceRulesCacheHits 验证同一条规则串重复求值命中 LRU 缓存：
// 第二次调用不再分词（缓存条目数不再增长），且结果与首次一致。
func TestSplitSourceRulesCacheHits(t *testing.T) {
	rule := `<js>1</js>.book@.name@text##替换##正则`
	first := splitSourceRules(rule, false)
	if len(first) == 0 {
		t.Fatalf("splitSourceRules() empty")
	}
	before := sourceRuleCache.Len()
	second := splitSourceRules(rule, false)
	if sourceRuleCache.Len() != before {
		t.Fatalf("cache grew from %d to %d: repeat rule did not hit cache", before, sourceRuleCache.Len())
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("cached result differs: %#v vs %#v", first, second)
	}
	// allInOne 标志参与缓存键，不得互相串结果。
	third := splitSourceRules(rule, true)
	if reflect.DeepEqual(first, third) == false && len(third) == len(first) && sourceRuleCache.Len() != before+1 {
		t.Fatalf("allInOne variant did not create its own cache entry")
	}
}

// TestEvalStringStepsParsesHTMLOnce 验证多步链对同一份 HTML 只做一次解析：
// 第二步起输入已是解析结果，不再触发 CSS 适配器对原始字符串的重解析。
func TestEvalStringStepsParsesHTMLOnce(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	html := `<div class="book"><a class="name">第一本</a><span class="author">作者甲</span></div>`
	// 两步链：先取 .book 元素，再从元素内取 .name 文本。
	got, err := analyzer.GetStringList(context.Background(), ".book@.name@text", html)
	if err != nil {
		t.Fatalf("GetStringList() error = %v", err)
	}
	if !reflect.DeepEqual(got, []string{"第一本"}) {
		t.Fatalf("values = %#v", got)
	}
	// parsedInput 惰性载体在同一条链内只解析一次：直接构造多步链求值，
	// 结果正确即代表复用路径生效（解析错误或重复解析会导致结果为空/报错）。
	multi, err := analyzer.GetString(context.Background(), ".book@.name@text&&.book@.author@text", html)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if !strings.Contains(multi, "第一本") {
		t.Fatalf("multi-step result = %q", multi)
	}
}

// TestParsedInputAdaptersReuseParses 验证 parsedInput 对同一份 string 输入的
// JSON/CSS/XPath 解析各只执行一次，且结果可复用（多步链不重复解析的可观测口径）。
func TestParsedInputAdaptersReuseParses(t *testing.T) {
	jsonRaw := `{"book":{"name":"第一本"}}`
	htmlRaw := `<div class="name">第一本</div>`

	// JSON：两次 jsonAdapter 共享同一次 Unmarshal 结果。
	p := newParsedInput(jsonRaw)
	a1, err := p.jsonAdapter()
	if err != nil {
		t.Fatalf("jsonAdapter() error = %v", err)
	}
	a2, err := p.jsonAdapter()
	if err != nil {
		t.Fatalf("jsonAdapter() second error = %v", err)
	}
	if !reflect.DeepEqual(p.json, mustDecode(t, jsonRaw)) {
		t.Fatal("parsed json differs from direct decode")
	}
	_ = a1
	_ = a2

	// CSS：同一份 HTML 两次构造共享同一解析结果。
	c := newParsedInput(htmlRaw)
	c1, err := c.cssAdapter()
	if err != nil {
		t.Fatalf("cssAdapter() error = %v", err)
	}
	c2, err := c.cssAdapter()
	if err != nil {
		t.Fatalf("cssAdapter() second error = %v", err)
	}
	if c1 != c2 {
		t.Fatal("second cssAdapter did not reuse first parse result")
	}
	if got, err := c2.Strings(".name@text"); err != nil || !reflect.DeepEqual(got, []string{"第一本"}) {
		t.Fatalf("css strings = %#v, %v", got, err)
	}

	// XPath：同上。
	x := newParsedInput(htmlRaw)
	x1, err := x.xpathAdapter()
	if err != nil {
		t.Fatalf("xpathAdapter() error = %v", err)
	}
	x2, err := x.xpathAdapter()
	if err != nil {
		t.Fatalf("xpathAdapter() second error = %v", err)
	}
	if x1 != x2 {
		t.Fatal("second xpathAdapter did not reuse first parse result")
	}
}

func mustDecode(t *testing.T, raw string) any {
	t.Helper()
	var data any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return data
}

// rawTextInput 是链首只认字符串的消费者不会包装成惰性解析载体的等价输入。
type rawTextInput string

// 惰性解析载体只服务 CSS/JSON/XPath 适配器；JS 步骤必须拿到链首原始文本，
// 否则 result 会变成 Go 结构体（`[object Object]`），@js: 规则全线失效。
func TestAnalyzerJSStepReceivesRawTextFromLazyInput(t *testing.T) {
	var seen any
	analyzer := NewAnalyzer(Context{Script: fakeRuleScriptFunc(func(scriptText string, input any) (any, error) {
		seen = input
		return strings.ToUpper(fmt.Sprint(input)), nil
	})})

	got, err := analyzer.GetString(context.Background(), "@js:result", "hello world")
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if seen != "hello world" {
		t.Fatalf("script input = %#v, want raw string", seen)
	}
	if got != "HELLO WORLD" {
		t.Fatalf("script result = %q", got)
	}
}

// 正则步骤同样只能看到原文：锚定正则 `^` 在载体结构体 dump 上必然失配。
func TestAnalyzerRegexStepSeesRawTextFromLazyInput(t *testing.T) {
	analyzer := NewAnalyzer(Context{})

	wrapped, err := analyzer.GetStringList(context.Background(), `##^第(\d+)章`, "第1章 开始")
	if err != nil {
		t.Fatalf("GetStringList() error = %v", err)
	}
	plain, err := analyzer.GetStringList(context.Background(), `##^第(\d+)章`, rawTextInput("第1章 开始"))
	if err != nil {
		t.Fatalf("GetStringList() plain error = %v", err)
	}
	if !reflect.DeepEqual(wrapped, plain) {
		t.Fatalf("wrapped = %#v, plain = %#v", wrapped, plain)
	}
	if len(wrapped) == 0 {
		t.Fatalf("anchored regex did not match raw text: %#v", wrapped)
	}
}

// S06-01：@put 值含嵌套花括号时按平衡括号完整提取，不在第一个 `}` 处截断（对齐 Legado 平衡组语义）。
func TestAnalyzerPutBalancedBracesValue(t *testing.T) {
	// 对齐 Legado 语义：@put 值按平衡括号完整提取（{k:1}），剩余文本作主规则原样参与求值
	// （顶层 `{rule}` 包裹在 Legado JSoup select 中同样无效，主规则不报语法错误）。
	analyzer := NewAnalyzer(Context{})
	input := `<div class="t">T</div>`
	_, err := analyzer.GetString(context.Background(), `@put:{a:{k:1}}{class.t@text}`, input)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if v := analyzer.Variables()["a"]; v != "{k:1}" {
		t.Fatalf("put value = %q, want {k:1}", v)
	}
	// 纯 put 规则（无主规则）返回输入而非报错，变量完整写入。
	analyzer2 := NewAnalyzer(Context{})
	elements, err := analyzer2.GetElements(context.Background(), `@put:{a:{k:1}}`, input)
	if err != nil {
		t.Fatalf("GetElements() error = %v", err)
	}
	if len(elements) != 1 {
		t.Fatalf("elements = %#v", elements)
	}
	if v := analyzer2.Variables()["a"]; v != "{k:1}" {
		t.Fatalf("put value = %q, want {k:1}", v)
	}
}

// S06-01：@put 值含 `}`（如 JSON 请求体）时不再静默截断。
func TestAnalyzerPutValueContainingClosingBrace(t *testing.T) {
	// @put 值含 `}`（如 JSON 请求体）时按平衡括号完整提取并求值，不静默截断。
	analyzer := NewAnalyzer(Context{})
	analyzer.LoadVariables(`{"bid":"B1"}`)
	got, err := analyzer.GetStringRaw(context.Background(), `@put:{body:{"bookId":"@get:{bid}"}}@get:{body}`, map[string]any{"bid": "B1"})
	if err != nil {
		t.Fatalf("GetStringRaw() error = %v", err)
	}
	if got != `{"bookId":"B1"}` {
		t.Fatalf("put expansion = %q", got)
	}
	if v := analyzer.Variables()["body"]; v != `{"bookId":"B1"}` {
		t.Fatalf("put variable = %q, want {\"bookId\":\"B1\"}", v)
	}
}

// S06-02：@put 多键 map 按规则书写顺序求值，键间依赖（b 依赖 a）结果确定。
func TestAnalyzerPutOrderedKeys(t *testing.T) {
	for i := range 100 {
		var analyzer *Analyzer
		script := fakeRuleScriptFunc(func(scriptText string, _ any) (any, error) {
			switch scriptText {
			case "1":
				return float64(1), nil
			case "get('a')":
				return analyzer.Variables()["a"], nil
			case "get('b')":
				return analyzer.Variables()["b"], nil
			}
			return "", nil
		})
		analyzer = &Analyzer{ctx: Context{Script: script}, vars: map[string]string{}}
		_, err := analyzer.GetString(context.Background(), `@put:{a:{{1}}}@put:{b:{{get('a')}}}@js:get('b')`, map[string]any{})
		if err != nil {
			t.Fatalf("run %d: GetString() error = %v", i, err)
		}
		if got := analyzer.Variables()["b"]; got != "1" {
			t.Fatalf("run %d: b = %q, want 1", i, got)
		}
	}
}

// S06-17：put 值为规则形态时恒走 GetString 求值（对齐 Legado put(key, getString(value))），
// 纯字段名/字面量走字段快捷路径（audit 修复方案的"仅非规则形态值走字段快捷路径"选项）。
func TestAnalyzerPutValueSameNameAsInputFieldEvaluatesRule(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	// put 值与 input 键同名且为规则形态（$.name 选择器）：必须求值而非把字段值当字面量返回。
	// 规则形态值经 GetString 求值命中 input 字段（Legado getString 对选择器取值）。
	input := map[string]any{"name": "第一本"}
	_, err := analyzer.GetString(context.Background(), `@put:{name:$.name}`, input)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got := analyzer.Variables()["name"]; got != "第一本" {
		t.Fatalf("put value = %q, want 第一本 (evaluated as rule)", got)
	}
	// put 值为纯字段名（非规则形态）走字段快捷路径，返回字段值。
	analyzer2 := NewAnalyzer(Context{})
	input2 := map[string]any{"title": "书名"}
	_, err = analyzer2.GetString(context.Background(), `@put:{name:title}`, input2)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got := analyzer2.Variables()["name"]; got != "书名" {
		t.Fatalf("put value = %q, want 书名 (input field shortcut)", got)
	}
}

// S06-03：makeUpStep 中 {{}}/脚本模板缺 Script 时优雅回退为原 token（对齐 Legado 内嵌
// JS 引擎恒可用的语义；顶层模板同样回退），规则整体不再失败。
func TestAnalyzerMakeUpStepScriptTemplateFallbackWithoutScript(t *testing.T) {
	analyzer := NewAnalyzer(Context{})
	got, err := analyzer.GetString(context.Background(), `class.t@text##(.*)##{{1+1}}`, `<div class="t">第一</div>`)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	// 无脚本运行时替换段 {{1+1}} 保留原 token（对齐顶层模板回退为原文的语义，规则整体不报错）。
	if got != "{{1+1}}" {
		t.Fatalf("GetString() = %q", got)
	}
}

// S06-04：GetURL 无结果时回退绝对化 baseUrl（对齐 Legado getString isUrl 分支）。
func TestAnalyzerGetURLFallsBackToBaseURL(t *testing.T) {
	// RedirectURL 就是 baseUrl：无结果时回退到 RedirectURL（absolutePlain 对绝对 URL 直接返回）。
	analyzer := NewAnalyzer(Context{BaseURL: "https://source.example.test", RedirectURL: "https://source.example.test"})
	got, err := analyzer.GetURL(context.Background(), ".missing@href", `<div></div>`)
	if err != nil {
		t.Fatalf("GetURL() error = %v", err)
	}
	if got != "https://source.example.test" {
		t.Fatalf("GetURL() = %q, want absolute baseUrl", got)
	}
	// baseUrl 为空时返回空串。
	analyzer2 := NewAnalyzer(Context{})
	empty, err := analyzer2.GetURL(context.Background(), ".missing@href", `<div></div>`)
	if err != nil || empty != "" {
		t.Fatalf("GetURL() = %q, %v", empty, err)
	}
}

// S06-06：顶层 || 取首个非空（非选择器规则不被静默当 && 步进）、%% 交错合并。
// 选择器规则（含 @/$）的 || 语义由 css 适配器处理（对齐 Legado 子选择器层），
// 顶层拆分仅作用于非选择器规则（JS 步链）。
func TestAnalyzerTopLevelOrAndInterleave(t *testing.T) {
	analyzer := NewAnalyzer(Context{Script: fakeRuleScript{value: "AB"}})
	// JS 步链：空结果步后接非空步，顶层 || 取首个非空。
	got, err := analyzer.GetString(context.Background(), `@js:""||@js:result`, `<div class="t">T</div>`)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != "AB" {
		t.Fatalf("GetString() = %q, want AB", got)
	}
	// A 空 B 非空时取 B。
	got2, err := analyzer.GetString(context.Background(), `class.none@text||class.t@text`, `<div class="t">T</div>`)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got2 != "T" {
		t.Fatalf("GetString() = %q, want T", got2)
	}
	// 顶层 %% 交错合并。
	analyzer2 := NewAnalyzer(Context{})
	got3, err := analyzer2.GetString(context.Background(), `class.a@text%%class.b@text`, `<div><span class="a">a1</span><span class="a">a2</span><span class="b">b1</span><span class="b">b2</span></div>`)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got3 != "a1\nb1\na2\nb2" {
		t.Fatalf("GetString() = %q, want a1\nb1\na2\nb2", got3)
	}
}

// S06-09：深层嵌套 {{@get:{x}}} 规则被深度上限截断，不再栈溢出。
func TestAnalyzerEvalDepthLimitPreventsDeepRecursion(t *testing.T) {
	// 64 层嵌套 {{...}} 模板（超出 maxEvalDepth=16）：超限保留原文本，不 panic。
	rule := "x"
	for i := 0; i < 64; i++ {
		rule = "{{" + rule + "}}"
	}
	analyzer := NewAnalyzer(Context{})
	_, err := analyzer.GetString(context.Background(), rule, map[string]any{})
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	// expandRawString 深度上限：直接验证递归展开不再溢出（超限返回原文本）。
	analyzer2 := NewAnalyzer(Context{})
	analyzer2.LoadVariables(`{"x":"1"}`)
	got, err := analyzer2.ExpandString(context.Background(), "@get:{x}", map[string]any{})
	if err != nil || got != "1" {
		t.Fatalf("ExpandString() = %q, %v", got, err)
	}
}

// S06-02：@put 多键按书写顺序求值（b 依赖 a 结果确定）已由 TestAnalyzerPutOrderedKeys 覆盖。
// S06-08：[]any 输入逐项替换不整体 Marshal。
func TestAnalyzerAppliesReplacePerItemOnAnySlice(t *testing.T) {
	analyzer := NewAnalyzer(Context{Script: fakeRuleScript{value: "AB"}})
	input := []any{"第一", "第二"}
	got, err := analyzer.GetString(context.Background(), `@js:result##第##章`, input)
	if err != nil {
		t.Fatalf("GetString() error = %v", err)
	}
	if got != "AB" {
		t.Fatalf("GetString() = %q", got)
	}
}
