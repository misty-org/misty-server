package db

import (
	"context"
	"testing"
)

// The Space's own events reach every member, and a family with no rule reaches no one. Neither
// answer reads the database, so no transaction is needed to ask.
func TestSpaceEventFamiliesWithoutAnItemRule(t *testing.T) {
	for eventType, want := range map[string]bool{
		"space.updated":     true,
		"member.joined":     true,
		"owner.transferred": true,
		"role.created":      true,
		"roles.reordered":   true,
		"drive.changed":     true,
		"drive.in_use":      true,
		"folder.updated":    false,
		"widget.created":    false,
		"sheet.updated":     false,
		"":                  false,
	} {
		got, err := spaceEventVisibleToUserTx(context.Background(), nil, "user", SpaceEvent{SpaceID: "space", EventType: eventType}, map[string]bool{})
		if err != nil {
			t.Fatalf("%q: %v", eventType, err)
		}
		if got != want {
			t.Errorf("%q visible = %v, want %v", eventType, got, want)
		}
	}
}

