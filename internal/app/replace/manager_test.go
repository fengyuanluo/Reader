package replaceapp

import (
	"context"
	"errors"
	"testing"

	"reader/internal/domain/replace"
)

func TestManagerTestPlainReplace(t *testing.T) {
	got, err := (&Manager{}).Test(context.Background(), replace.ReplaceRule{Pattern: "foo", Replacement: "bar", IsRegex: false}, "foo foo")
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if got != "bar bar" {
		t.Fatalf("got = %q", got)
	}
}

func TestManagerTestRegexReplace(t *testing.T) {
	got, err := (&Manager{}).Test(context.Background(), replace.ReplaceRule{Pattern: `foo(\d+)`, Replacement: "bar$1", IsRegex: true}, "foo12")
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if got != "bar12" {
		t.Fatalf("got = %q", got)
	}
}

func TestManagerTestInvalidRegex(t *testing.T) {
	_, err := (&Manager{}).Test(context.Background(), replace.ReplaceRule{Pattern: "(", IsRegex: true}, "text")
	if !errors.Is(err, replace.ErrInvalidRegex) {
		t.Fatalf("err = %v", err)
	}
}

func TestManagerTestRegexRejectsOversizedInput(t *testing.T) {
	text := make([]byte, maxRegexInputBytes+1)
	for i := range text {
		text[i] = 'a'
	}
	_, err := (&Manager{}).Test(context.Background(), replace.ReplaceRule{Pattern: "a", Replacement: "b", IsRegex: true}, string(text))
	if !errors.Is(err, ErrRegexLimitExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestManagerTestRegexRejectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&Manager{}).Test(ctx, replace.ReplaceRule{Pattern: "a", Replacement: "b", IsRegex: true}, "a")
	if !errors.Is(err, ErrRegexLimitExceeded) {
		t.Fatalf("err = %v", err)
	}
}

var errFakeRuleNotFound = errors.New("replace rule not found")

// Update 对不存在的 ID 必须返回 NotFound（HTTP 映射 404），不能静默 upsert 出一条新规则。
func TestManagerUpdateReturnsNotFoundForMissingRule(t *testing.T) {
	repo := &fakeManagerRepo{}
	manager := NewManager(repo)

	rule := &replace.ReplaceRule{Pattern: "ad", Replacement: ""}
	if _, err := manager.Update(context.Background(), 99, rule); !errors.Is(err, errFakeRuleNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if repo.saved != nil {
		t.Fatalf("missing rule must not be saved, saved = %#v", repo.saved)
	}
}

func TestManagerUpdateSavesExistingRule(t *testing.T) {
	repo := &fakeManagerRepo{existing: map[int64]*replace.ReplaceRule{7: {ID: 7, Pattern: "old"}}}
	manager := NewManager(repo)

	got, err := manager.Update(context.Background(), 7, &replace.ReplaceRule{Pattern: "new"})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got == nil || got.ID != 7 || got.Pattern != "new" {
		t.Fatalf("updated rule = %#v", got)
	}
}

// Create 不再自己用时间戳生成主键，ID 由仓储层生成（避免并发创建时主键碰撞）。
func TestManagerCreateUsesRepositoryGeneratedID(t *testing.T) {
	repo := &fakeManagerRepo{}
	manager := NewManager(repo)

	created, err := manager.Create(context.Background(), &replace.ReplaceRule{Pattern: "ad"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created == nil || created.ID != fakeManagerAssignedID {
		t.Fatalf("created rule = %#v, want repository assigned ID %d", created, fakeManagerAssignedID)
	}
}

const fakeManagerAssignedID int64 = 42

type fakeManagerRepo struct {
	existing map[int64]*replace.ReplaceRule
	saved    *replace.ReplaceRule
}

func (r *fakeManagerRepo) Save(ctx context.Context, rule *replace.ReplaceRule) error {
	if rule.ID == 0 {
		rule.ID = fakeManagerAssignedID
	}
	if r.existing == nil {
		r.existing = map[int64]*replace.ReplaceRule{}
	}
	copy := *rule
	r.existing[rule.ID] = &copy
	r.saved = &copy
	return nil
}

func (r *fakeManagerRepo) FindByID(ctx context.Context, id int64) (*replace.ReplaceRule, error) {
	rule, ok := r.existing[id]
	if !ok {
		return nil, errFakeRuleNotFound
	}
	return rule, nil
}

func (r *fakeManagerRepo) List(ctx context.Context, filter replace.ReplaceRuleFilter) ([]*replace.ReplaceRule, int, error) {
	return nil, 0, nil
}

func (r *fakeManagerRepo) Delete(ctx context.Context, id int64) error { return nil }

func (r *fakeManagerRepo) FindEnabledByTitleScope(ctx context.Context, bookName, origin string) ([]*replace.ReplaceRule, error) {
	return nil, nil
}

func (r *fakeManagerRepo) FindEnabledByContentScope(ctx context.Context, bookName, origin string) ([]*replace.ReplaceRule, error) {
	return nil, nil
}
