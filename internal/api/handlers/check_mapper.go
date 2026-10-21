package handlers

import (
	"time"

	"reader/internal/api/generated"
	checkapp "reader/internal/app/check"
	"reader/internal/domain/task"
)

func checkOptionsFromDTO(dto *generated.CheckOptions) checkapp.Options {
	opts := checkapp.Options{Keyword: checkapp.DefaultCheckKeyword, Timeout: checkapp.DefaultTimeout, CheckSearch: true, CheckDiscovery: true, CheckInfo: true, CheckToc: true, CheckContent: true}
	if dto == nil {
		return opts
	}
	if dto.Keyword != nil {
		opts.Keyword = *dto.Keyword
	}
	if dto.CheckSearch != nil {
		opts.CheckSearch = *dto.CheckSearch
	}
	if dto.CheckDiscovery != nil {
		opts.CheckDiscovery = *dto.CheckDiscovery
	}
	if dto.CheckInfo != nil {
		opts.CheckInfo = *dto.CheckInfo
	}
	if dto.CheckToc != nil {
		opts.CheckToc = *dto.CheckToc
	}
	if dto.CheckContent != nil {
		opts.CheckContent = *dto.CheckContent
	}
	if dto.TimeoutMs != nil && *dto.TimeoutMs > 0 {
		opts.Timeout = time.Duration(*dto.TimeoutMs) * time.Millisecond
	}
	return opts
}

// itemStatusFromDTO 把 GetCheckTaskParams.Status 转成领域条目状态；nil/未知值返回 nil（不过滤）。
func itemStatusFromDTO(params generated.GetCheckTaskParams) *task.CheckTaskItemStatus {
	if params.Status == nil {
		return nil
	}
	status := task.CheckTaskItemStatus(*params.Status)
	switch status {
	case task.CheckItemPending, task.CheckItemRunning, task.CheckItemPassed, task.CheckItemFailed, task.CheckItemSkipped, task.CheckItemCancelled:
		return &status
	}
	return nil
}

// mapCheckEvent 把领域校验事件映射为 DTO；Item 为 nil 时省略（S03-10 摘要事件）。
func mapCheckEvent(event *task.CheckEvent) generated.CheckEvent {
	dto := generated.CheckEvent{
		TaskId:  event.TaskID,
		Type:    generated.CheckEventType(event.Type),
		Message: event.Message,
		TraceId: event.TraceID,
	}
	if event.SourceID != "" {
		dto.SourceId = &event.SourceID
	}
	if event.Item != nil {
		item := mapCheckItem(event.Item)
		dto.Item = &item
	}
	return dto
}

func mapCheckTask(item *task.CheckTask) generated.CheckTask {
	dto := generated.CheckTask{
		CreatedAt: item.CreatedAt,
		Finished:  item.Finished,
		Items:     mapCheckItems(item.Items),
		Status:    generated.TaskStatus(item.Status),
		TaskId:    item.TaskID,
		Total:     item.Total,
	}
	if item.TraceID != "" {
		dto.TraceId = &item.TraceID
	}
	if item.CompletedAt != nil {
		dto.CompletedAt = item.CompletedAt
	}
	return dto
}

func mapCheckItems(items []*task.CheckTaskItem) []generated.CheckTaskItem {
	out := make([]generated.CheckTaskItem, 0, len(items))
	for _, item := range items {
		out = append(out, mapCheckItem(item))
	}
	return out
}

func mapCheckItem(item *task.CheckTaskItem) generated.CheckTaskItem {
	dto := generated.CheckTaskItem{
		SourceId: item.SourceID,
		Status:   generated.CheckTaskItemStatus(item.Status),
	}
	if item.BookSourceURL != "" {
		dto.BookSourceUrl = &item.BookSourceURL
	}
	if item.BookSourceName != "" {
		dto.BookSourceName = &item.BookSourceName
	}
	if item.FailureCode != "" {
		code := generated.CheckFailureCode(item.FailureCode)
		dto.FailureCode = &code
	}
	if item.FailureClass != "" {
		dto.FailureClass = &item.FailureClass
	}
	if item.Message != "" {
		dto.Message = &item.Message
	}
	if item.RespondTime != 0 {
		dto.RespondTime = &item.RespondTime
	}
	if item.KeywordUsed != "" {
		dto.KeywordUsed = &item.KeywordUsed
	}
	if item.TimeoutMS != 0 {
		dto.TimeoutMs = &item.TimeoutMS
	}
	if len(item.Stages) > 0 {
		stages := make([]generated.CheckStageResult, 0, len(item.Stages))
		for _, stage := range item.Stages {
			mapped := generated.CheckStageResult{
				Flow:   generated.CheckStageFlow(stage.Flow),
				Stage:  generated.CheckStageName(stage.Stage),
				Status: generated.CheckTaskItemStatus(stage.Status),
			}
			if stage.FailureCode != "" {
				code := generated.CheckFailureCode(stage.FailureCode)
				mapped.FailureCode = &code
			}
			if stage.FailureClass != "" {
				mapped.FailureClass = &stage.FailureClass
			}
			if stage.Message != "" {
				mapped.Message = &stage.Message
			}
			if stage.ElapsedMS != 0 {
				mapped.ElapsedMs = &stage.ElapsedMS
			}
			stages = append(stages, mapped)
		}
		dto.Stages = &stages
	}
	if item.TraceID != "" {
		dto.TraceId = &item.TraceID
	}
	return dto
}
