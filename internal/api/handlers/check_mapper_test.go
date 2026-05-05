package handlers

import (
	"testing"

	"reader/internal/api/generated"
	diagnosticapp "reader/internal/app/diagnostic"
	"reader/internal/domain/task"
)

func TestMapCheckItemIncludesFailureClass(t *testing.T) {
	item := &task.CheckTaskItem{
		SourceID:      "source-1",
		Status:        task.CheckItemFailed,
		FailureCode:   diagnosticapp.FailureSearchFailed,
		FailureClass:  diagnosticapp.FailureClassSourceSearchEmptyResult,
		Message:       "empty search result",
		RespondTime:   42,
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
}
