package rule

import (
	"encoding/json"
	"testing"
)

// S17-05：Flexible[T] 表驱动测试（null、字符串包裹 JSON、裸 JSON、Raw 回写、Value-only 序列化）。
func TestFlexibleUnmarshal(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantVal  bool
		wantName string
		wantRaw  string
	}{
		{name: "null", raw: `null`, wantVal: false},
		{name: "bare object", raw: `{"name":"a"}`, wantVal: true, wantName: "a", wantRaw: `{"name":"a"}`},
		{name: "string-wrapped object", raw: `"{\"name\":\"b\"}"`, wantVal: true, wantName: "b"},
		{name: "string-wrapped invalid json", raw: `"not-json"`, wantVal: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f Flexible[struct {
				Name string `json:"name"`
			}]
			err := json.Unmarshal([]byte(tt.raw), &f)
			if tt.name == "string-wrapped invalid json" {
				if err == nil {
					t.Fatalf("Unmarshal() = nil error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if tt.wantVal != (f.Value != nil) {
				t.Fatalf("Value = %v, wantVal %v", f.Value != nil, tt.wantVal)
			}
			if tt.wantVal && f.Value.Name != tt.wantName {
				t.Fatalf("Name = %q, want %q", f.Value.Name, tt.wantName)
			}
		})
	}
}

func TestFlexibleMarshalRawPriority(t *testing.T) {
	var f Flexible[struct {
		Name string `json:"name"`
	}]
	if err := json.Unmarshal([]byte(`{"name":"a"}`), &f); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	// Raw 回写优先（保持原文本，含空格/顺序）。
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(raw) != `{"name":"a"}` {
		t.Fatalf("Marshal() = %q", raw)
	}
	// Value-only：清空 Raw 后按 Value 序列化。
	f.Raw = nil
	raw, err = json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(raw) != `{"name":"a"}` {
		t.Fatalf("Marshal(value-only) = %q", raw)
	}
	// null 序列化：无 Raw 且 Value 为 nil。
	var empty Flexible[struct{}]
	raw, err = json.Marshal(empty)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(raw) != "null" {
		t.Fatalf("Marshal(empty) = %q, want null", raw)
	}
}
