package db

import (
	"context"
	"testing"
)

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
