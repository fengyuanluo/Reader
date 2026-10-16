package sourceapp

import (
	"context"

	"reader/internal/domain/source"
	"reader/internal/infra/requestoption"
	"reader/internal/infra/script"
)

func RequestEvaluator(runtime *script.Runtime, src *source.BookSource, bindings script.Bindings) requestoption.Evaluator {
	if runtime == nil {
		return nil
	}
	return func(ctx context.Context, scriptText string, input any) (any, error) {
		local := make(script.Bindings, len(bindings)+3)
		for key, value := range bindings {
			local[key] = value
		}
		local["result"] = input
		local["source"] = runtime.SourceBindings(ctx, src)
		local["__source"] = src
		return runtime.EvalWithBindings(ctx, scriptText, local)
	}
}
