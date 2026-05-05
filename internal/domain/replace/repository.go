package replace

import "context"

type ReplaceRuleFilter struct {
	Page     int
	PageSize int
}

type ReplaceRuleRepository interface {
	Save(ctx context.Context, rule *ReplaceRule) error
	FindByID(ctx context.Context, id int64) (*ReplaceRule, error)
	List(ctx context.Context, filter ReplaceRuleFilter) ([]*ReplaceRule, int, error)
	Delete(ctx context.Context, id int64) error
	FindEnabledByTitleScope(ctx context.Context, bookName, origin string) ([]*ReplaceRule, error)
	FindEnabledByContentScope(ctx context.Context, bookName, origin string) ([]*ReplaceRule, error)
}
