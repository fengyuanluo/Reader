package urlrule

import (
	"strconv"
	"strings"

	"reader/internal/domain/fetch"
)

// LooksLikeFormBody 判断请求体是否按 Legado AnalyzeUrl 的约定当作表单编码发送：
// JSON / XML 结构（以 { [ < 开头）不视为表单，其余含 = 或 & 的文本按表单编码。
// 搜索、书籍信息、正文与脚本四条抓取链路共用这一份实现，避免行为漂移。
func LooksLikeFormBody(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}
	if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") || strings.HasPrefix(body, "<") {
		return false
	}
	return strings.Contains(body, "=") || strings.Contains(body, "&")
}

// ApplyRetryOption 解析 URL 选项 retry（Legado AnalyzeUrl.UrlOption.retry），
// 按宽化语义接受数字与数字字符串；非正数或无法解析的值不改变请求。
func ApplyRetryOption(req *fetch.Request, value any) {
	if req == nil {
		return
	}
	switch typed := value.(type) {
	case float64:
		if typed > 0 {
			req.Retry = int(typed)
		}
	case int:
		if typed > 0 {
			req.Retry = typed
		}
	case int64:
		if typed > 0 {
			req.Retry = int(typed)
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil && parsed > 0 {
			req.Retry = parsed
		}
	}
}
