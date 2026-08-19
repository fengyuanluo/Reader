package handlers

import (
	"encoding/json"
	"errors"

	"reader/internal/api/generated"
	discoveryapp "reader/internal/app/discovery"
	searchapp "reader/internal/app/search"
	sourceapp "reader/internal/app/source"
	"reader/internal/domain/book"
	"reader/internal/domain/rule"
	"reader/internal/domain/source"
)

func bookSourceFromDTO(dto generated.BookSource) (*source.BookSource, error) {
	src := &source.BookSource{
		SourceID:         dto.SourceId,
		BookSourceURL:    dto.BookSourceUrl,
		BookSourceName:   dto.BookSourceName,
		BookSourceType:   dto.BookSourceType,
		CustomOrder:      dto.CustomOrder,
		Enabled:          dto.Enabled,
		EnabledExplore:   dto.EnabledExplore,
		EnabledCookieJar: dto.EnabledCookieJar,
		LastUpdateTime:   dto.LastUpdateTime,
		RuleExplore:      flexibleFromRuleObject[rule.ExploreRule](dto.RuleExplore),
		RuleSearch:       flexibleFromRuleObject[rule.SearchRule](dto.RuleSearch),
		RuleBookInfo:     flexibleFromRuleObject[rule.BookInfoRule](dto.RuleBookInfo),
		RuleToc:          flexibleFromRuleObject[rule.TocRule](dto.RuleToc),
		RuleContent:      flexibleFromRuleObject[rule.ContentRule](dto.RuleContent),
	}
	if dto.EventListener != nil {
		src.EventListener = *dto.EventListener
	}
	if dto.CustomButton != nil {
		src.CustomButton = *dto.CustomButton
	}
	src.BookSourceGroup = stringValue(dto.BookSourceGroup)
	src.BookURLPattern = stringValue(dto.BookUrlPattern)
	src.ConcurrentRate = stringValue(dto.ConcurrentRate)
	src.CoverDecodeJS = stringValue(dto.CoverDecodeJs)
	src.ExploreScreen = stringValue(dto.ExploreScreen)
	src.ExploreURL = stringValue(dto.ExploreUrl)
	src.Header = stringValue(dto.Header)
	src.JSLib = stringValue(dto.JsLib)
	src.LoginCheckJS = stringValue(dto.LoginCheckJs)
	src.LoginUI = stringValue(dto.LoginUi)
	src.LoginURL = stringValue(dto.LoginUrl)
	src.BookSourceComment = stringValue(dto.BookSourceComment)
	src.SearchURL = stringValue(dto.SearchUrl)
	src.VariableComment = stringValue(dto.VariableComment)
	if dto.RespondTime != nil {
		src.RespondTime = *dto.RespondTime
	}
	if dto.Weight != nil {
		src.Weight = *dto.Weight
	}
	if dto.RuleReview != nil {
		raw, _ := dto.RuleReview.MarshalJSON()
		src.RuleReview = raw
	}
	raw, _ := json.Marshal(dto)
	src.RawJSON = raw
	if err := src.Normalize(); err != nil {
		return nil, err
	}
	return src, nil
}

func mapBookSource(src *source.BookSource) generated.BookSource {
	dto := generated.BookSource{
		SourceId:         src.SourceID,
		BookSourceUrl:    src.BookSourceURL,
		BookSourceName:   src.BookSourceName,
		BookSourceType:   src.BookSourceType,
		CustomOrder:      src.CustomOrder,
		Enabled:          src.Enabled,
		EnabledExplore:   src.EnabledExplore,
		EnabledCookieJar: src.EnabledCookieJar,
		EventListener:    boolPtr(src.EventListener),
		CustomButton:     boolPtr(src.CustomButton),
		LastUpdateTime:   src.LastUpdateTime,
		RuleExplore:      ruleObjectFromFlexible(src.RuleExplore),
		RuleSearch:       ruleObjectFromFlexible(src.RuleSearch),
		RuleBookInfo:     ruleObjectFromFlexible(src.RuleBookInfo),
		RuleToc:          ruleObjectFromFlexible(src.RuleToc),
		RuleContent:      ruleObjectFromFlexible(src.RuleContent),
	}
	dto.BookSourceGroup = stringPtr(src.BookSourceGroup)
	dto.BookUrlPattern = stringPtr(src.BookURLPattern)
	dto.ConcurrentRate = stringPtr(src.ConcurrentRate)
	dto.CoverDecodeJs = stringPtr(src.CoverDecodeJS)
	dto.ExploreScreen = stringPtr(src.ExploreScreen)
	dto.ExploreUrl = stringPtr(src.ExploreURL)
	dto.Header = stringPtr(src.Header)
	dto.JsLib = stringPtr(src.JSLib)
	dto.LoginCheckJs = stringPtr(src.LoginCheckJS)
	dto.LoginUi = stringPtr(src.LoginUI)
	dto.LoginUrl = stringPtr(src.LoginURL)
	dto.BookSourceComment = stringPtr(src.BookSourceComment)
	dto.SearchUrl = stringPtr(src.SearchURL)
	dto.VariableComment = stringPtr(src.VariableComment)
	dto.RespondTime = int64Ptr(src.RespondTime)
	dto.Weight = intPtr(src.Weight)
	if len(src.RuleReview) > 0 {
		var review generated.BookSource_RuleReview
		if err := review.UnmarshalJSON(src.RuleReview); err == nil {
			dto.RuleReview = &review
		}
	}
	return dto
}

func mapSourceSummaries(items []*source.BookSource) []generated.BookSourceSummary {
	out := make([]generated.BookSourceSummary, 0, len(items))
	for _, item := range items {
		out = append(out, mapBookSourceSummary(item))
	}
	return out
}

func mapBookSourceSummary(src *source.BookSource) generated.BookSourceSummary {
	return generated.BookSourceSummary{
		SourceId:        src.SourceID,
		BookSourceUrl:   src.BookSourceURL,
		BookSourceName:  src.BookSourceName,
		BookSourceGroup: stringPtr(src.BookSourceGroup),
		BookSourceType:  src.BookSourceType,
		CustomOrder:     src.CustomOrder,
		Enabled:         src.Enabled,
		EnabledExplore:  src.EnabledExplore,
		RespondTime:     int64Ptr(src.RespondTime),
		Weight:          intPtr(src.Weight),
	}
}

func importInputFromDTO(dto generated.ImportBookSourcesRequest) (sourceapp.ImportInput, error) {
	input := sourceapp.ImportInput{Options: importOptionsFromDTO(dto.Options)}
	if dto.RemoteUrl != nil {
		input.RemoteURL = *dto.RemoteUrl
	}
	if dto.Payload != nil {
		raw, err := dto.Payload.MarshalJSON()
		if err != nil {
			return input, err
		}
		input.Payload = raw
	}
	if input.RemoteURL == "" && len(input.Payload) == 0 {
		return input, errors.New("payload or remoteUrl is required")
	}
	return input, nil
}

func importOptionsFromDTO(dto *generated.ImportOptions) sourceapp.ImportOptions {
	if dto == nil {
		return sourceapp.ImportOptions{}
	}
	options := sourceapp.ImportOptions{}
	if dto.KeepExistingName != nil {
		options.KeepExistingName = *dto.KeepExistingName
	}
	if dto.KeepExistingGroup != nil {
		options.KeepExistingGroup = *dto.KeepExistingGroup
	}
	if dto.KeepExistingEnabled != nil {
		options.KeepExistingEnabled = *dto.KeepExistingEnabled
	}
	if dto.GroupMode != nil {
		options.GroupMode = string(*dto.GroupMode)
	}
	if dto.GroupName != nil {
		options.GroupName = *dto.GroupName
	}
	if dto.UpdatePolicy != nil {
		options.UpdatePolicy = string(*dto.UpdatePolicy)
	}
	return options
}

func mapImportResult(result *sourceapp.ImportResult, traceID string) generated.ImportBookSourcesResult {
	items := make([]generated.ImportBookSourceItemResult, 0, len(result.Items))
	for _, item := range result.Items {
		mapped := generated.ImportBookSourceItemResult{
			BookSourceName: stringPtr(item.BookSourceName),
			BookSourceUrl:  stringPtr(item.BookSourceURL),
			SourceId:       stringPtr(item.SourceID),
			Status:         generated.ImportBookSourceItemResultStatus(item.Status),
		}
		if item.Error != "" {
			mapped.Error = &generated.Error{Code: "import_failed", Message: item.Error, TraceId: traceID}
		}
		items = append(items, mapped)
	}
	remoteErrors := make([]generated.ImportRemoteError, 0, len(result.RemoteErrors))
	for _, remote := range result.RemoteErrors {
		remoteErrors = append(remoteErrors, generated.ImportRemoteError{
			Url:   remote.URL,
			Error: generated.Error{Code: "fetch_failed", Message: remote.Error, TraceId: traceID},
		})
	}
	mapped := generated.ImportBookSourcesResult{
		Added:   result.Added,
		Updated: result.Updated,
		Skipped: result.Skipped,
		Failed:  result.Failed,
		Items:   items,
	}
	if len(remoteErrors) > 0 {
		mapped.RemoteErrors = &remoteErrors
	}
	return mapped
}

func mapSearchBooks(items []book.SearchBook) []generated.SearchBook {
	out := make([]generated.SearchBook, 0, len(items))
	for _, item := range items {
		out = append(out, generated.SearchBook{
			BookUrl:            item.BookURL,
			Origin:             item.Origin,
			OriginName:         item.OriginName,
			Type:               item.Type,
			Name:               item.Name,
			Author:             item.Author,
			Kind:               stringPtr(item.Kind),
			CoverUrl:           stringPtr(item.CoverURL),
			Intro:              stringPtr(item.Intro),
			WordCount:          stringPtr(item.WordCount),
			LatestChapterTitle: stringPtr(item.LatestChapterTitle),
			TocUrl:             stringPtr(item.TocURL),
			Variable:           stringPtr(item.Variable),
			OriginOrder:        item.OriginOrder,
			Origins:            append([]string(nil), item.Origins...),
		})
	}
	return out
}

func mapSourceWarnings(warnings []searchapp.SourceWarning) []generated.SourceWarning {
	out := make([]generated.SourceWarning, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, generated.SourceWarning{
			SourceId:       warning.SourceID,
			BookSourceUrl:  stringPtr(warning.BookSourceURL),
			BookSourceName: stringPtr(warning.BookSourceName),
			Code:           warning.Code,
			Message:        warning.Message,
		})
	}
	return out
}

func mapDiscoveryWarnings(warnings []discoveryapp.Warning) []generated.SourceWarning {
	out := make([]generated.SourceWarning, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, generated.SourceWarning{
			SourceId:       warning.SourceID,
			BookSourceUrl:  stringPtr(warning.BookSourceURL),
			BookSourceName: stringPtr(warning.BookSourceName),
			Code:           warning.Code,
			Message:        warning.Message,
		})
	}
	return out
}

func flexibleFromRuleObject[T any](obj *generated.RuleObject) rule.Flexible[T] {
	var flex rule.Flexible[T]
	if obj == nil {
		return flex
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return flex
	}
	_ = json.Unmarshal(raw, &flex)
	return flex
}

func ruleObjectFromFlexible[T any](flex rule.Flexible[T]) *generated.RuleObject {
	if flex.Raw == nil {
		return nil
	}
	var obj generated.RuleObject
	if err := json.Unmarshal(flex.Raw, &obj); err != nil {
		return nil
	}
	return &obj
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func int64Ptr(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func intPtr(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}
