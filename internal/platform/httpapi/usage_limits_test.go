package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/misty-org/misty-server/internal/platform/db"
)

func TestUsageLimitReachesTheAgentAsACodedToolMessage(t *testing.T) {
	err := usageLimitToolError(db.UsageLimitError{Metric: db.UsageAppActions, Limit: 5})
	if !errors.Is(err, db.ErrAgentToolboxNotAttempted) {
		t.Fatal("a refused app action must say it was not attempted")
	}
	result := mcpToolError(err)
	data, _ := json.Marshal(result)
	if !result.IsError || !strings.Contains(string(data), "app_action_limit_reached: This account used its 5 app actions") {
		t.Fatalf("tool result = %s", data)
	}
	if other := errors.New("boom"); usageLimitToolError(other) != other {
		t.Fatal("other errors must pass through")
	}
	if usageLimitToolError(nil) != nil {
		t.Fatal("nil must stay nil")
	}
}
