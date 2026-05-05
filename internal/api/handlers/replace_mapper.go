package handlers

import (
	"encoding/json"
	"errors"

	"reader/internal/api/generated"
	"reader/internal/domain/replace"
)

func replaceRuleFromDTO(dto generated.ReplaceRule) replace.ReplaceRule {
	return replace.ReplaceRule{
		ID:                 dto.Id,
		Name:               dto.Name,
		Group:              stringValue(dto.Group),
		Pattern:            dto.Pattern,
		Replacement:        dto.Replacement,
		Scope:              stringValue(dto.Scope),
		ScopeTitle:         dto.ScopeTitle,
		ScopeContent:       dto.ScopeContent,
		ExcludeScope:       stringValue(dto.ExcludeScope),
		IsEnabled:          dto.IsEnabled,
		IsRegex:            dto.IsRegex,
		TimeoutMillisecond: dto.TimeoutMillisecond,
		Order:              dto.Order,
	}
}

func replaceRuleFromUnion(union generated.ReplaceRuleTestRequest_Rule) (replace.ReplaceRule, error) {
	if dto, err := union.AsReplaceRule(); err == nil {
		return replaceRuleFromDTO(dto), nil
	}
	if encoded, err := union.AsReplaceRuleTestRequestRule1(); err == nil {
		var dto generated.ReplaceRule
		if err := json.Unmarshal([]byte(encoded), &dto); err != nil {
			return replace.ReplaceRule{}, err
		}
		return replaceRuleFromDTO(dto), nil
	}
	return replace.ReplaceRule{}, errors.New("rule must be a ReplaceRule object or JSON string")
}

func mapReplaceRule(rule *replace.ReplaceRule) generated.ReplaceRule {
	return generated.ReplaceRule{
		Id:                 rule.ID,
		Name:               rule.Name,
		Group:              stringPtr(rule.Group),
		Pattern:            rule.Pattern,
		Replacement:        rule.Replacement,
		Scope:              stringPtr(rule.Scope),
		ScopeTitle:         rule.ScopeTitle,
		ScopeContent:       rule.ScopeContent,
		ExcludeScope:       stringPtr(rule.ExcludeScope),
		IsEnabled:          rule.IsEnabled,
		IsRegex:            rule.IsRegex,
		TimeoutMillisecond: rule.ValidTimeoutMillisecond(),
		Order:              rule.Order,
	}
}

func mapReplaceRules(rules []*replace.ReplaceRule) []generated.ReplaceRule {
	out := make([]generated.ReplaceRule, 0, len(rules))
	for _, rule := range rules {
		out = append(out, mapReplaceRule(rule))
	}
	return out
}
