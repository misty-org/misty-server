package db

import (
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSpaceTaskCursorRoundTrip(t *testing.T) {
	for _, offset := range []int{0, 1, 200, 1_000_000} {
		got, err := TestingDecodeTaskCursor(TestingEncodeTaskCursor(offset))
		if err != nil || got != offset {
			t.Fatalf("cursor round trip for %d: got %d, err %v", offset, got, err)
		}
	}
}

func TestSpaceTaskCursorRejectsInvalidOffsets(t *testing.T) {
	for _, cursor := range []string{"not-base64!", TestingEncodeTaskCursor(-1), TestingEncodeTaskCursor(1_000_001)} {
		if _, err := TestingDecodeTaskCursor(cursor); err == nil {
			t.Fatalf("expected cursor %q to be rejected", cursor)
		}
	}
}

func TestValidateSpaceTaskDefaultsPriority(t *testing.T) {
	item := SpaceTask{Title: "Ship Tasks", Status: "todo", DueTimezone: "UTC"}
	if err := TestingValidateSpaceTask(&item); err != nil {
		t.Fatalf("validate task: %v", err)
	}
	if item.Priority != "medium" {
		t.Fatalf("default priority = %q, want medium", item.Priority)
	}
}

func TestValidateSpaceTaskRejectsUnknownPriority(t *testing.T) {
	item := SpaceTask{Title: "Ship Tasks", Status: "todo", Priority: "urgent", DueTimezone: "UTC"}
	if err := TestingValidateSpaceTask(&item); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("validate priority error = %v, want ErrSpaceInvalid", err)
	}
}
