package sourceapp

import (
	"context"

	"reader/internal/domain/book"
	domainrule "reader/internal/domain/rule"
	domainsource "reader/internal/domain/source"
	ruleinfra "reader/internal/infra/rule"
	"reader/internal/infra/script"
)

var _ domainrule.ScriptRuntime = ScriptAdapter{}

// ScriptAdapter 是 domainrule.ScriptRuntime 的共享实现：把规则脚本需要的绑定一次性注入 goja 运行时。
// 搜索、发现、书籍信息/目录与正文四条链路共用它，差异只通过字段表达，避免各包各写一份导致绑定集漂移。
//
// 绑定集对齐 Legado AnalyzeRule.evalJS（AnalyzeRule.kt:780-786）：result/src/baseUrl/source/book/chapter/title/nextChapterUrl
// 恒定注入，未使用的链路拿到空值，脚本读取时不区分“未注入”和“为空”。
type ScriptAdapter struct {
	Runtime *script.Runtime
	BaseURL string
	Source  *domainsource.BookSource
	Src     any
	// Book 为脚本 book 绑定：搜索/发现链路传 *book.SearchBook，书籍信息/目录/正文链路传 *book.Book。
	Book any
	// Chapter 为脚本 chapter 绑定；title 由 ChapterTitleBinding 从章节标题派生。
	Chapter *book.BookChapter
	// NextChapterURL 只在正文链路有值，其余链路为空串。
	NextChapterURL string
}

// Bindings 返回本次脚本调用的绑定集合：注入的绑定与四条链路无关，只有取值不同。
func (s ScriptAdapter) Bindings(input any) script.Bindings {
	return script.Bindings{
		"result":         input,
		"src":            s.Src,
		"baseUrl":        s.BaseURL,
		"source":         s.Source,
		"book":           s.Book,
		"chapter":        s.Chapter,
		"title":          script.ChapterTitleBinding(s.Chapter),
		"nextChapterUrl": s.NextChapterURL,
	}
}

func (s ScriptAdapter) Eval(ctx context.Context, scriptText string, input any) (any, error) {
	if s.Runtime == nil {
		return nil, ruleinfra.ErrScriptUnavailable
	}
	return s.Runtime.EvalWithBindings(ctx, scriptText, s.Bindings(input))
}
