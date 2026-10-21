package repositories

import (
	"context"
	"testing"
)

// TestReplaceRuleScopeExactMatching 覆盖 S11-05 的历史误匹配问题：SQL LIKE 子串
// 语义下书名 "斗" 会误命中 scope "斗破"，应用层按项精确匹配后不再命中。
func TestReplaceRuleScopeExactMatching(t *testing.T) {
	ctx := context.Background()
	repo := NewReplaceRuleRepository(openRepoTestDB(t))
	// scope "斗破"：书名 "斗" 不命中（子串），书名 "斗破" 命中（精确）。
	daopò := replaceRule(1, "daopo", 1)
	daopò.ScopeContent = true
	daopò.Scope = "斗破"
	if err := repo.Save(ctx, daopò); err != nil {
		t.Fatalf("Save(daopo) error = %v", err)
	}
	// scope "玄幻,都市"：多分组按分隔符拆项精确匹配。
	grouped := replaceRule(2, "grouped", 2)
	grouped.ScopeContent = true
	grouped.Scope = "玄幻,都市"
	if err := repo.Save(ctx, grouped); err != nil {
		t.Fatalf("Save(grouped) error = %v", err)
	}
	// exclude_scope "斗破"：书名 "斗破" 被排除。
	excluded := replaceRule(3, "excluded", 3)
	excluded.ScopeContent = true
	excluded.ExcludeScope = "斗破"
	if err := repo.Save(ctx, excluded); err != nil {
		t.Fatalf("Save(excluded) error = %v", err)
	}

	cases := []struct {
		name     string
		bookName string
		wantIDs  []int64
	}{
		// rule 3 只设 exclude_scope "斗破"：书名不是 "斗破" 时不被排除，命中。
		{name: "substring does not match", bookName: "斗", wantIDs: []int64{3}},
		{name: "exact match", bookName: "斗破", wantIDs: []int64{1}},
		{name: "grouped exact item", bookName: "玄幻", wantIDs: []int64{2, 3}},
		{name: "grouped second item", bookName: "都市", wantIDs: []int64{2, 3}},
		{name: "no scope items", bookName: "科幻", wantIDs: []int64{3}},
		{name: "exclude scope excludes book", bookName: "斗破", wantIDs: []int64{1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules, err := repo.FindEnabledByContentScope(ctx, tc.bookName, "origin-none")
			if err != nil {
				t.Fatalf("FindEnabledByContentScope() error = %v", err)
			}
			got := make([]int64, 0, len(rules))
			for _, rule := range rules {
				got = append(got, rule.ID)
			}
			if len(got) != len(tc.wantIDs) {
				t.Fatalf("book %q matched %v, want %v", tc.bookName, got, tc.wantIDs)
			}
			for i := range tc.wantIDs {
				if got[i] != tc.wantIDs[i] {
					t.Fatalf("book %q matched %v, want %v", tc.bookName, got, tc.wantIDs)
				}
			}
		})
	}
}

// TestReplaceRuleEmptyScopeMatchesAll 覆盖空 scope 语义：不限制（全部命中）。
func TestReplaceRuleEmptyScopeMatchesAll(t *testing.T) {
	ctx := context.Background()
	repo := NewReplaceRuleRepository(openRepoTestDB(t))
	rule := replaceRule(1, "any", 1)
	rule.ScopeContent = true
	rule.Scope = ""
	if err := repo.Save(ctx, rule); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	rules, err := repo.FindEnabledByContentScope(ctx, "任意书名", "origin")
	if err != nil {
		t.Fatalf("FindEnabledByContentScope() error = %v", err)
	}
	if len(rules) != 1 || rules[0].ID != 1 {
		t.Fatalf("rules = %#v, want rule 1", rules)
	}
}

// TestReplaceRuleOriginScopeMatches 覆盖 scope 项与源名精确相等的命中路径。
func TestReplaceRuleOriginScopeMatches(t *testing.T) {
	ctx := context.Background()
	repo := NewReplaceRuleRepository(openRepoTestDB(t))
	rule := replaceRule(1, "origin-scope", 1)
	rule.ScopeContent = true
	rule.Scope = "笔趣阁"
	if err := repo.Save(ctx, rule); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	rules, err := repo.FindEnabledByContentScope(ctx, "无关书名", "笔趣阁")
	if err != nil {
		t.Fatalf("FindEnabledByContentScope() error = %v", err)
	}
	if len(rules) != 1 || rules[0].ID != 1 {
		t.Fatalf("rules = %#v, want rule 1 by origin", rules)
	}
}
