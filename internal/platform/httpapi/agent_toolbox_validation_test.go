package api

import (
	"errors"
	"testing"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

func TestValidationErrorsLeaveWritesRetryable(t *testing.T) {
	for _, err := range []error{agent.ErrInvalidRequest("pass space for this change"), agenttools.ErrArgumentsInvalid} {
		_, journaled := notAttemptedOnValidation(nil, err)
		var invalid agent.ErrInvalidRequest
		if !errors.Is(journaled, db.ErrAgentToolboxNotAttempted) || !(errors.Is(journaled, err) || errors.As(journaled, &invalid)) {
			t.Fatalf("validation error %v journaled as %v", err, journaled)
		}
	}
	if _, journaled := notAttemptedOnValidation(nil, errors.New("collaboration write failed")); errors.Is(journaled, db.ErrAgentToolboxNotAttempted) {
		t.Fatal("an execution failure must keep its unknown outcome")
	}
}
