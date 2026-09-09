package capabilities

import (
	"github.com/google/uuid"
	"time"
)

// A wait has a durable identity but is not an external effect journal entry.
func RoutineWaitID(userID, runID, stepID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("misty:routine:wait\x00"+userID+"\x00"+runID+"\x00"+stepID)).String()
}
func RoutineWaitUntil(value string) (time.Time, error) {
	until, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, ErrInvalid
	}
	// Workflow timers use milliseconds. Round up so conversion cannot wake early.
	rounded := until.UTC().Truncate(time.Millisecond)
	if rounded.Before(until) {
		rounded = rounded.Add(time.Millisecond)
	}
	return rounded, nil
}
