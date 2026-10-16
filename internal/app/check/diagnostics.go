package checkapp

import (
	"context"
	"strings"

	"reader/internal/domain/source"
	"reader/internal/domain/task"
)

type SourceRepository interface {
	FindByID(ctx context.Context, id string) (*source.BookSource, error)
	Save(ctx context.Context, src *source.BookSource) error
}

type DiagnosticsUpdater struct {
	repo SourceRepository
}

func NewDiagnosticsUpdater(repo SourceRepository) *DiagnosticsUpdater {
	return &DiagnosticsUpdater{repo: repo}
}

func (u *DiagnosticsUpdater) Apply(ctx context.Context, item *task.CheckTaskItem) error {
	src, err := u.repo.FindByID(ctx, item.SourceID)
	if err != nil {
		return err
	}
	src.RespondTime = item.RespondTime
	src.BookSourceComment = removeErrorComment(src.BookSourceComment)
	src.BookSourceGroup = removeFailureGroups(src.BookSourceGroup)
	if item.Status == task.CheckItemFailed {
		group := groupForFailure(item.FailureCode, item.Path)
		src.BookSourceGroup = addGroup(src.BookSourceGroup, group)
		if item.Message != "" {
			src.BookSourceComment = addErrorComment(src.BookSourceComment, formatFailureComment(item))
		}
	}
	return u.repo.Save(ctx, src)
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
