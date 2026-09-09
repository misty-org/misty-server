package capabilities

import (
	"testing"
	"time"
)

func TestRoutineWaitTimeAndIdentity(t *testing.T) {
	until, err := RoutineWaitUntil("2026-09-08T09:00:00.0000001-07:00")
	if err != nil || until.Format(time.RFC3339Nano) != "2026-09-08T16:00:00.001Z" {
		t.Fatalf("early timer: %v %v", until, err)
	}
	if _, err := RoutineWaitUntil("tomorrow at nine"); err == nil {
		t.Fatal("ambiguous local time accepted")
	}
	id := RoutineWaitID("user", "run", "wait")
	if !ValidID(id) || id != RoutineWaitID("user", "run", "wait") || id == RoutineWaitID("other-user", "run", "wait") || id == RoutineWaitID("user", "other-run", "wait") {
		t.Fatal("wait identity escaped admission")
	}
}
