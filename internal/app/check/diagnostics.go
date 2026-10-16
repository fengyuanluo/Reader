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
		group := groupForFailure(item.FailureCode)
		src.BookSourceGroup = addGroup(src.BookSourceGroup, group)
		if item.Message != "" {
			src.BookSourceComment = addErrorComment(src.BookSourceComment, formatFailureComment(item))
		}
	}
	return u.repo.Save(ctx, src)
}

func groupForFailure(code string) string {
	switch code {
	case FailureTimeout:
		return "校验超时"
	case FailureJSFailed:
		return "js失效"
	case FailureSearchFailed:
		return "搜索失效"
	case FailureDiscoveryFailed:
		return "发现失效"
	case FailureTocFailed:
		return "目录失效"
	case FailureContentFailed:
		return "正文失效"
	default:
		return "网站失效"
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
		if strings.Contains(value, "失效") || value == "校验超时" {
			continue
		}
		out = append(out, value)
	}
	return strings.Join(out, ",")
}

func splitGroups(groups string) []string {
	return source.SplitGroups(groups)
}
