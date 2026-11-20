package api

import (
	"testing"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

func TestSearchKindsTakeEveryMadeKindByNameOrSection(t *testing.T) {
	all, ok := spaceSearchKinds("")
	if !ok || !all["message"] || !all["note"] {
		t.Fatalf("searching everything = %v %v", all, ok)
	}
	picked, ok := spaceSearchKinds("notes, message")
	if !ok || !picked["note"] || !picked["message"] {
		t.Fatalf("picked kinds = %v %v", picked, ok)
	}
	for _, removed := range []string{"note,secrets", "drawing", "decks", "workbook"} {
		if _, ok := spaceSearchKinds(removed); ok {
			t.Fatalf("%q was accepted", removed)
		}
	}
}

func TestHistoryWindowFollowsTheLevel(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour)
	if !itemHistoryKept(db.SpaceItemLimits{}, old, now) {
		t.Fatal("unlimited history dropped an old version")
	}
	days := int64(30)
	limits := db.SpaceItemLimits{HistoryDays: &days}
	if itemHistoryKept(limits, old, now) {
		t.Fatal("a version past the level's history window was kept")
	}
	if !itemHistoryKept(limits, now.Add(-29*24*time.Hour), now) {
		t.Fatal("a version inside the window was dropped")
	}
	for _, id := range []string{"v_0123456789abcdef0123", "v_x"} {
		if !itemHistoryVersionPattern.MatchString(id) {
			t.Errorf("version id %q refused", id)
		}
	}
	for _, id := range []string{"", "../x", "v x", "v/1"} {
		if itemHistoryVersionPattern.MatchString(id) {
			t.Errorf("version id %q accepted", id)
		}
	}
}
