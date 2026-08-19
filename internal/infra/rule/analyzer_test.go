package ruleinfra

import (
	"context"
	"errors"
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
	got, err := analyzer.GetStringList(context.Background(), `.book@a@href`, `<div class="book"><a href="../c/1.html">go</a></div>`)
	if err != nil {
		t.Fatalf("GetStringList() error = %v", err)
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
	url, err := analyzer.GetString(context.Background(), "href", elements[0])
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
	image, err := analyzer.GetString(context.Background(), ".infoText@img@data-src", elements[0])
	if err != nil {
		t.Fatalf("GetString(image) error = %v", err)
	}
	if image != "https://example.test/1.jpg" {
		t.Fatalf("image = %q", image)
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
