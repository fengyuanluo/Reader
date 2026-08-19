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
	if item.Status == task.CheckItemPassed {
		src.BookSourceComment = removeErrorComment(src.BookSourceComment)
		src.BookSourceGroup = removeFailureGroups(src.BookSourceGroup)
	} else if item.Status == task.CheckItemFailed && isDefinitiveSourceFailure(item.FailureClass) {
		src.BookSourceComment = removeErrorComment(src.BookSourceComment)
		src.BookSourceGroup = removeFailureGroups(src.BookSourceGroup)
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
	case FailureInfoFailed:
		return "详情失效"
	case FailureTocFailed:
		return "目录失效"
	case FailureContentFailed:
		return "正文失效"
	default:
		return "网站失效"
	}
}

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
		if strings.Contains(value, "失效") || value == "校验超时" {
			continue
		}
		out = append(out, value)
	}
	return strings.Join(out, ",")
}

func splitGroups(groups string) []string {
	parts := strings.FieldsFunc(groups, func(r rune) bool {
		return r == ',' || r == ';' || r == '，' || r == '；'
	})
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
