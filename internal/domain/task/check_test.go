package task

import (
	"errors"
	"testing"
)

func TestValidateCheckTaskTransitionAllowsExpectedStateMachine(t *testing.T) {
	allowed := [][2]CheckTaskStatus{
		{"", CheckTaskPending},
		{CheckTaskPending, CheckTaskRunning},
		{CheckTaskPending, CheckTaskCancelled},
		{CheckTaskRunning, CheckTaskSucceeded},
		{CheckTaskRunning, CheckTaskFailed},
		{CheckTaskRunning, CheckTaskCancelled},
	}
	for _, pair := range allowed {
		if err := ValidateCheckTaskTransition(pair[0], pair[1]); err != nil {
			t.Fatalf("transition %q -> %q error = %v", pair[0], pair[1], err)
		}
	}
}

func TestValidateCheckTaskTransitionRejectsInvalidStateMachine(t *testing.T) {
	invalid := [][2]CheckTaskStatus{
		{CheckTaskPending, CheckTaskSucceeded},
		{CheckTaskSucceeded, CheckTaskRunning},
		{CheckTaskFailed, CheckTaskRunning},
		{CheckTaskCancelled, CheckTaskRunning},
	}
	for _, pair := range invalid {
		if err := ValidateCheckTaskTransition(pair[0], pair[1]); !errors.Is(err, ErrInvalidCheckTaskTransition) {
			t.Fatalf("transition %q -> %q error = %v", pair[0], pair[1], err)
		}
	}
}
