package repositories

import (
	"context"
	"errors"
	"testing"

	"reader/internal/domain/replace"
)

func TestReplaceRuleRepositoryCRUDAndSort(t *testing.T) {
	ctx := context.Background()
	repo := NewReplaceRuleRepository(openRepoTestDB(t))
	first := replaceRule(1, "second", 2)
	second := replaceRule(2, "first", 1)
	if err := repo.Save(ctx, first); err != nil {
		t.Fatalf("Save first error = %v", err)
	}
	if err := repo.Save(ctx, second); err != nil {
		t.Fatalf("Save second error = %v", err)
	}

	got, err := repo.FindByID(ctx, 1)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if got.Name != "second" {
		t.Fatalf("FindByID() = %#v", got)
	}
	list, total, err := repo.List(ctx, replace.ReplaceRuleFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 2 || list[0].ID != 2 || list[1].ID != 1 {
		t.Fatalf("list total=%d items=%#v", total, list)
	}
	if err := repo.Delete(ctx, 1); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repo.FindByID(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FindByID after delete error = %v, want ErrNotFound", err)
	}
}

func TestReplaceRuleRepositoryScopes(t *testing.T) {
	ctx := context.Background()
	repo := NewReplaceRuleRepository(openRepoTestDB(t))
	title := replaceRule(1, "title", 1)
	title.ScopeTitle = true
	title.ScopeContent = false
	title.Scope = "Book"
	content := replaceRule(2, "content", 2)
	content.ScopeTitle = false
	content.ScopeContent = true
	content.Scope = "https://example.test"
	disabled := replaceRule(3, "disabled", 3)
	disabled.IsEnabled = false
	excluded := replaceRule(4, "excluded", 4)
	excluded.ScopeContent = true
	excluded.ExcludeScope = "Book"
	for _, rule := range []*replace.ReplaceRule{title, content, disabled, excluded} {
		if err := repo.Save(ctx, rule); err != nil {
			t.Fatalf("Save(%s) error = %v", rule.Name, err)
		}
	}

	titleRules, err := repo.FindEnabledByTitleScope(ctx, "Book", "https://example.test")
	if err != nil {
		t.Fatalf("FindEnabledByTitleScope() error = %v", err)
	}
	if len(titleRules) != 1 || titleRules[0].ID != 1 {
		t.Fatalf("title rules = %#v", titleRules)
	}
	contentRules, err := repo.FindEnabledByContentScope(ctx, "Book", "https://example.test")
	if err != nil {
		t.Fatalf("FindEnabledByContentScope() error = %v", err)
	}
	if len(contentRules) != 1 || contentRules[0].ID != 2 {
		t.Fatalf("content rules = %#v", contentRules)
	}
}

// ID 为 0 时由 SQLite 生成主键并回写（R-123：避免上层用时间戳生成主键在并发创建时碰撞）。
func TestReplaceRuleRepositoryAssignsIDWhenZero(t *testing.T) {
	ctx := context.Background()
	repo := NewReplaceRuleRepository(openRepoTestDB(t))
	rule := replaceRule(0, "auto", 1)
	if err := repo.Save(ctx, rule); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if rule.ID == 0 {
		t.Fatalf("Save() did not backfill rule ID")
	}
	found, err := repo.FindByID(ctx, rule.ID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if found.Name != "auto" {
		t.Fatalf("FindByID() = %#v", found)
	}

	other := replaceRule(0, "auto2", 2)
	if err := repo.Save(ctx, other); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if other.ID == rule.ID {
		t.Fatalf("two inserts got the same ID %d", other.ID)
	}
}

func replaceRule(id int64, name string, order int) *replace.ReplaceRule {
	return &replace.ReplaceRule{
		ID:                 id,
		Name:               name,
		Pattern:            "foo",
		Replacement:        "bar",
		ScopeContent:       true,
		IsEnabled:          true,
		IsRegex:            false,
		TimeoutMillisecond: 3000,
		Order:              order,
	}
}
