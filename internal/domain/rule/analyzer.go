package rule

import "context"

type ScriptRuntime interface {
	Eval(ctx context.Context, script string, input any) (any, error)
}

type Analyzer interface {
	GetString(ctx context.Context, rule string, input any) (string, error)
	GetStringList(ctx context.Context, rule string, input any) ([]string, error)
	GetElements(ctx context.Context, rule string, input any) ([]any, error)
}
