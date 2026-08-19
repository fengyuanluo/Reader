package handlers

import (
	"testing"

	"reader/internal/api/generated"
	checkapp "reader/internal/app/check"
	diagnosticapp "reader/internal/app/diagnostic"
	"reader/internal/domain/task"
)

func TestMapCheckItemIncludesFailureClass(t *testing.T) {
	item := &task.CheckTaskItem{
		SourceID:     "source-1",
		Status:       task.CheckItemFailed,
		FailureCode:  diagnosticapp.FailureSearchFailed,
		FailureClass: diagnosticapp.FailureClassSourceSearchEmptyResult,
		Message:      "empty search result",
		RespondTime:  42,
		KeywordUsed:  "源关键词",
		TimeoutMS:    180000,
		Stages: []task.CheckStageResult{{
			Flow: task.CheckFlowSearch, Stage: task.CheckStageSearch, Status: task.CheckItemFailed,
			FailureCode: diagnosticapp.FailureSearchFailed, FailureClass: diagnosticapp.FailureClassSourceSearchEmptyResult,
		}},
		TraceID:       "trace-1",
		BookSourceURL: "https://source.test",
	}

	dto := mapCheckItem(item)

	if dto.FailureCode == nil || *dto.FailureCode != generated.CheckFailureCode(diagnosticapp.FailureSearchFailed) {
		t.Fatalf("failureCode = %#v", dto.FailureCode)
	}
	if dto.FailureClass == nil || *dto.FailureClass != diagnosticapp.FailureClassSourceSearchEmptyResult {
		t.Fatalf("failureClass = %#v", dto.FailureClass)
	}
	if dto.Message == nil || *dto.Message != "empty search result" {
		t.Fatalf("message = %#v", dto.Message)
	}
	if dto.KeywordUsed == nil || *dto.KeywordUsed != "源关键词" || dto.TimeoutMs == nil || *dto.TimeoutMs != 180000 || dto.Stages == nil || len(*dto.Stages) != 1 {
		t.Fatalf("dto = %#v", dto)
	}
}

func TestCheckOptionsFromDTOAppliesDocumentedTimeoutDefault(t *testing.T) {
	if got := checkOptionsFromDTO(nil).Timeout; got != checkapp.DefaultTimeout {
		t.Fatalf("timeout = %s", got)
	}
}

func TestCheckEventTypeMatchesTaskState(t *testing.T) {
	tests := map[task.CheckTaskStatus]generated.CheckEventType{
		task.CheckTaskPending:   generated.CheckStarted,
		task.CheckTaskRunning:   generated.CheckProgress,
		task.CheckTaskSucceeded: generated.CheckCompleted,
		task.CheckTaskFailed:    generated.CheckCompleted,
		task.CheckTaskCancelled: generated.CheckCancelled,
	}
	for status, want := range tests {
		if got := checkEventType(status); got != want {
			t.Fatalf("status %s: got %s want %s", status, got, want)
		}
	}
}
