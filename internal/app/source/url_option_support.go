package sourceapp

import (
	"errors"
	"fmt"
	"strings"
)

var ErrUnsupportedURLOption = errors.New("unsupported url option")

func ValidateSupportedURLOptions(opts map[string]any, supportedKeys ...string) error {
	if len(opts) == 0 {
		return nil
	}
	allowed := map[string]struct{}{}
	for _, key := range supportedKeys {
		allowed[strings.ToLower(strings.TrimSpace(key))] = struct{}{}
	}
	for key, value := range opts {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, ok := allowed[normalized]; ok {
			continue
		}
		if isZeroLikeOptionValue(value) {
			continue
		}
		return fmt.Errorf("%w: %s", ErrUnsupportedURLOption, key)
	}
	return nil
}

func isZeroLikeOptionValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case bool:
		return !typed
	case float64:
		return typed == 0
	case int:
		return typed == 0
	case int64:
		return typed == 0
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}
