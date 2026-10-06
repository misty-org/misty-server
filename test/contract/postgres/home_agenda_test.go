package db

import (
	"context"
	"testing"
	"time"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)


import ()

func TestHomeAgendaMergesSpacesInOneReadWithoutLeakingOthers(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Home Owner", "home-agenda-owner@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := database.CreateUser("Stranger", "home-agenda-stranger@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().Truncate(24 * time.Hour)
	first := createTestSpace(t, database, ctx, owner.ID, "First")
	second := createTestSpace(t, database, ctx, owner.ID, "Second")
	foreign := createTestSpace(t, database, ctx, stranger.ID, "Foreign")
	add := func(user, space, title, status string, at time.Time) {
		t.Helper()
		if _, err := database.CreateSpaceTask(ctx, user, SpaceTask{SpaceID: space, Title: title, Status: status, Priority: "low", DueAt: &at}); err != nil {
			t.Fatal(err)
		}
	}
	add(owner.ID, first.ID, "late", "todo", day.Add(15*time.Hour))
	add(owner.ID, second.ID, "early", "todo", day.Add(9*time.Hour))
	add(owner.ID, second.ID, "middle", "todo", day.Add(12*time.Hour))
	add(owner.ID, first.ID, "canceled", "canceled", day.Add(8*time.Hour))
	add(stranger.ID, foreign.ID, "private", "todo", day.Add(7*time.Hour))
	entries, err := database.HomeAgenda(ctx, owner.ID, day, day.Add(24*time.Hour), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Title != "early" || entries[1].Title != "middle" || entries[0].SpaceID != second.ID || entries[0].SpaceName != "Second" {
		t.Fatalf("merged agenda = %#v", entries)
	}
	all, err := database.HomeAgenda(ctx, owner.ID, day, day.Add(24*time.Hour), 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range all {
		if entry.Title == "private" || entry.Title == "canceled" {
			t.Fatalf("agenda leaked or kept a closed entry: %#v", entry)
		}
	}
	if len(all) != 3 {
		t.Fatalf("open entries = %d", len(all))
	}
}
