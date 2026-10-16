package task

import (
	"slices"
	"testing"
)

func TestCanTransitionCheckTaskAllowsExpectedStateMachine(t *testing.T) {
	allowed := [][2]CheckTaskStatus{
		{"", CheckTaskPending},
		{CheckTaskPending, CheckTaskRunning},
		{CheckTaskPending, CheckTaskCancelled},
		{CheckTaskRunning, CheckTaskSucceeded},
		{CheckTaskRunning, CheckTaskFailed},
		{CheckTaskRunning, CheckTaskCancelled},
	}
	for _, pair := range allowed {
		if !CanTransitionCheckTask(pair[0], pair[1]) {
			t.Fatalf("transition %q -> %q rejected", pair[0], pair[1])
		}
	}
}

func TestCanTransitionCheckTaskRejectsInvalidStateMachine(t *testing.T) {
	invalid := [][2]CheckTaskStatus{
		{CheckTaskPending, CheckTaskSucceeded},
		{CheckTaskSucceeded, CheckTaskRunning},
		{CheckTaskFailed, CheckTaskRunning},
		{CheckTaskCancelled, CheckTaskRunning},
	}
	for _, pair := range invalid {
		if CanTransitionCheckTask(pair[0], pair[1]) {
			t.Fatalf("transition %q -> %q allowed", pair[0], pair[1])
		}
	}
}

func TestCheckTaskPredecessors(t *testing.T) {
	cases := []struct {
		to   CheckTaskStatus
		want []CheckTaskStatus
	}{
		{CheckTaskPending, nil},
		{CheckTaskRunning, []CheckTaskStatus{CheckTaskPending}},
		{CheckTaskSucceeded, []CheckTaskStatus{CheckTaskRunning}},
		{CheckTaskFailed, []CheckTaskStatus{CheckTaskRunning}},
		{CheckTaskCancelled, []CheckTaskStatus{CheckTaskPending, CheckTaskRunning}},
	}
	for _, tc := range cases {
		if got := CheckTaskPredecessors(tc.to); !slices.Equal(got, tc.want) {
			t.Fatalf("CheckTaskPredecessors(%q) = %v, want %v", tc.to, got, tc.want)
		}
	}
}
