package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestBookSourceCreateAppliesLegadoDefaults 覆盖 R-058 在 HTTP 路径上的缺口：
// 请求体缺省 enabled/enabledExplore/enabledCookieJar/respondTime 时按 Legado BookSource 默认值补齐，
// 显式写出的值（含 false）保持不变，且与导入路径行为一致。
func TestBookSourceCreateAppliesLegadoDefaults(t *testing.T) {
	router := newSourceHandlerTestRouter(t)

	cases := []struct {
		name            string
		body            string
		wantEnabled     bool
		wantExplore     bool
		wantCookieJar   bool
		wantRespondTime float64
	}{
		{
			name:            "缺省字段补齐 Legado 默认值",
			body:            `{"bookSourceUrl":"https://defaults.example.test","bookSourceName":"缺省"}`,
			wantEnabled:     true,
			wantExplore:     true,
			wantCookieJar:   true,
			wantRespondTime: 180000,
		},
		{
			name:            "显式 false 不被覆盖",
			body:            `{"bookSourceUrl":"https://explicit.example.test","bookSourceName":"显式","enabled":false,"enabledExplore":false,"enabledCookieJar":false,"respondTime":5000}`,
			wantEnabled:     false,
			wantExplore:     false,
			wantCookieJar:   false,
			wantRespondTime: 5000,
		},
		{
			name:            "显式 null 按缺省处理",
			body:            `{"bookSourceUrl":"https://null.example.test","bookSourceName":"空值","enabled":null,"respondTime":null}`,
			wantEnabled:     true,
			wantExplore:     true,
			wantCookieJar:   true,
			wantRespondTime: 180000,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := authedRequest(router, http.MethodPost, "/api/v1/book-sources", tc.body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
			}
			var created map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
				t.Fatalf("decode create: %v", err)
			}
			if got := created["enabled"]; got != tc.wantEnabled {
				t.Fatalf("enabled = %#v, want %v (body=%s)", got, tc.wantEnabled, rec.Body.String())
			}
			if got := created["enabledExplore"]; got != tc.wantExplore {
				t.Fatalf("enabledExplore = %#v, want %v", got, tc.wantExplore)
			}
			if got := created["enabledCookieJar"]; got != tc.wantCookieJar {
				t.Fatalf("enabledCookieJar = %#v, want %v", got, tc.wantCookieJar)
			}
			if got := created["respondTime"]; got != tc.wantRespondTime {
				t.Fatalf("respondTime = %#v, want %v", got, tc.wantRespondTime)
			}
		})
	}
}
