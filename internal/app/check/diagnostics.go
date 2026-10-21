package checkapp

import (
	"context"
	"errors"
	"strings"
	"sync"

	"reader/internal/domain/source"
	"reader/internal/domain/task"
)

// SourceDiagnosticsWriter 按 sourceId 窄列更新书源诊断字段（S03-02/S03-03），
// 由支持窄列更新的仓储实现（如 SQLite 的 CheckRepository）。
type SourceDiagnosticsWriter interface {
	UpdateSourceDiagnostics(ctx context.Context, sourceID string, respondTime int64, transform func(group, comment string) (string, string)) error
}

type DiagnosticsUpdater struct {
	// writer 是窄列更新仓储：诊断只写 respond_time/分组/注释三列，
	// 不整行读改写书源（S03-02）。装配侧应传入 CheckRepository。
	writer SourceDiagnosticsWriter
	// applyMu 序列化同一进程内对书源诊断行的读改写：多任务并行校验同一源时，
	// 诊断分组的读-改-写不会互相交错覆盖（S03-03）。
	applyMu sync.Mutex
}

func NewDiagnosticsUpdater(writer SourceDiagnosticsWriter) *DiagnosticsUpdater {
	return &DiagnosticsUpdater{writer: writer}
}

func (u *DiagnosticsUpdater) Apply(ctx context.Context, item *task.CheckTaskItem) error {
	if u.writer == nil {
		return errors.New("诊断更新器未配置窄列写入仓储")
	}
	u.applyMu.Lock()
	defer u.applyMu.Unlock()
	// 窄列更新路径：只写 respond_time/分组/注释三列，
	// 不整行重写，也不会覆盖并发修改的其他列（S03-02/S03-03）。
	return u.applyNarrow(ctx, u.writer, item)
}

// applyNarrow 是窄列更新路径：变换在 SQL 侧读出的当前值上完成，回调给出新的分组与注释。
func (u *DiagnosticsUpdater) applyNarrow(ctx context.Context, writer SourceDiagnosticsWriter, item *task.CheckTaskItem) error {
	transform := func(group, comment string) (string, string) {
		src := &source.BookSource{BookSourceGroup: group, BookSourceComment: comment}
		u.applyToSource(src, item)
		return src.BookSourceGroup, src.BookSourceComment
	}
	return writer.UpdateSourceDiagnostics(ctx, item.SourceID, item.RespondTime, transform)
}

// applyToSource 把条目结果变换到书源诊断字段；窄列路径与整行路径共用同一套变换规则。
func (u *DiagnosticsUpdater) applyToSource(src *source.BookSource, item *task.CheckTaskItem) {
	src.RespondTime = item.RespondTime
	if item.Status == task.CheckItemPassed {
		src.BookSourceComment = removeErrorComment(src.BookSourceComment)
		src.BookSourceGroup = removeFailureGroups(src.BookSourceGroup)
	} else if item.Status == task.CheckItemFailed && isDefinitiveSourceFailure(item.FailureClass) {
		src.BookSourceComment = removeErrorComment(src.BookSourceComment)
		src.BookSourceGroup = removeFailureGroups(src.BookSourceGroup)
		group := groupForFailure(item.FailureCode, item.Path)
		src.BookSourceGroup = addGroup(src.BookSourceGroup, group)
		if item.Message != "" {
			src.BookSourceComment = addErrorComment(src.BookSourceComment, formatFailureComment(item))
		}
	}
}

// groupForFailure 按 Legado CheckSourceService 的分组命名生成书源分组：
// 目录/正文失败区分搜索链路与发现链路，规则缺失单独成组。
func groupForFailure(code string, path task.CheckItemPath) string {
	switch code {
	case FailureTimeout:
		return "校验超时"
	case FailureJSFailed:
		return "js失效"
	case FailureSearchFailed:
		return "搜索失效"
	case FailureDiscoveryFailed:
		return "发现失效"
	case FailureInfoFailed:
		return "详情失效"
	case FailureSearchRuleEmpty:
		return "搜索链接规则为空"
	case FailureDiscoveryRuleEmpty:
		return "发现规则为空"
	case FailureTocFailed:
		return pathPrefix(path) + "目录失效"
	case FailureContentFailed:
		return pathPrefix(path) + "正文失效"
	default:
		return "网站失效"
	}
}

func pathPrefix(path task.CheckItemPath) string {
	if path == task.CheckPathDiscovery {
		return "发现"
	}
	return "搜索"
}

// isDefinitiveSourceFailure 判断失败是否已经足够确定，可以把书源写进“失效”分组和 // Error: 注释：
// 只有确定的源空结果与规则失败可以污染书源元数据，网络/HTTP/浏览器边界等失败只保留任务诊断。
func isDefinitiveSourceFailure(class string) bool {
	if class == "" {
		return true
	}
	switch class {
	case "source_search_empty_result",
		"source_discovery_empty_result",
		"source_toc_empty",
		"source_content_empty",
		"source_download_urls_empty",
		"source_rule_script_syntax_error",
		"source_rule_script_failed":
		return true
	default:
		return false
	}
}

func addErrorComment(comment string, message string) string {
	if strings.TrimSpace(comment) == "" {
		return "// Error: " + message
	}
	return "// Error: " + message + "\n\n" + comment
}

func formatFailureComment(item *task.CheckTaskItem) string {
	message := strings.TrimSpace(item.Message)
	if item.FailureClass == "" {
		return message
	}
	if message == "" {
		return item.FailureClass
	}
	return item.FailureClass + ": " + message
}

func removeErrorComment(comment string) string {
	parts := strings.Split(comment, "\n\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.HasPrefix(part, "// Error: ") {
			continue
		}
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "\n\n")
}

func addGroup(groups string, group string) string {
	existing := splitGroups(groups)
	for _, value := range existing {
		if value == group {
			return strings.Join(existing, ",")
		}
	}
	existing = append(existing, group)
	return strings.Join(existing, ",")
}

func removeFailureGroups(groups string) string {
	existing := splitGroups(groups)
	out := make([]string, 0, len(existing))
	for _, value := range existing {
		if isCheckGroup(value) {
			continue
		}
		out = append(out, value)
	}
	return strings.Join(out, ",")
}

// isCheckGroup 判断分组是否由校验流程写入。
// 与 Legado BookSource.getInvalidGroupNames 一致：包含“失效”或等于“校验超时”的分组都属于校验分组；
// 另外 Reader/Legado 的规则缺失分组在规则补齐后的下一次校验中也要被清掉。
func isCheckGroup(value string) bool {
	return strings.Contains(value, "失效") || value == "校验超时" || value == "搜索链接规则为空" || value == "发现规则为空"
}

func splitGroups(groups string) []string {
	return source.SplitGroups(groups)
}
