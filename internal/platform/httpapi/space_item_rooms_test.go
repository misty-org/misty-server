package api

import (
	"testing"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

func TestSearchKindsTakeEveryMadeKindByNameOrSection(t *testing.T) {
	all, ok := spaceSearchKinds("")
	if !ok {
		t.Fatal("an empty kinds list was refused")
	}
	for _, kind := range []string{"message", "note", "drawing", "deck", "workbook"} {
		if !all[kind] {
			t.Errorf("searching everything leaves out %s", kind)
		}
	}
	picked, ok := spaceSearchKinds("note, drawing,decks,workbook")
	if !ok || !picked["note"] || !picked["drawing"] || !picked["deck"] || !picked["workbook"] || picked["message"] {
		t.Fatalf("picked kinds = %v %v", picked, ok)
	}
	if _, ok := spaceSearchKinds("note,secrets"); ok {
		t.Fatal("an unknown kind was accepted")
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

func TestMadeItemRoutesServeOnlySharedKinds(t *testing.T) {
	for _, name := range []string{"deck", "workbook", "decks"} {
		if _, ok := madeItemKind(name); !ok {
			t.Errorf("%s is not served", name)
		}
	}
	for _, name := range []string{"note", "drawing", "image", ""} {
		if _, ok := madeItemKind(name); ok {
			t.Errorf("%s is served by the shared made-item routes", name)
		}
	}
	deck, _ := madeItemKind("deck")
	if madeItemParam(deck) != "deckID" {
		t.Fatalf("deck route parameter = %q", madeItemParam(deck))
	}
}
