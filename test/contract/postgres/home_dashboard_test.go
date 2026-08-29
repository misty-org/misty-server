package db

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestHomeDashboardPersistsPerAccountActivityAndRecentApps(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Home Owner", "home-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Home data")
	if err != nil {
		t.Fatal(err)
	}
	dateKey := time.Now().UTC().Format("2006-01-02")
	if _, err := database.RecordHomeVisit(ctx, owner.ID, space.ID, dateKey); err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.RecordHomeVisit(ctx, owner.ID, space.ID, dateKey)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Activity[dateKey] != 2 {
		t.Fatalf("visit count = %d, want 2", snapshot.Activity[dateKey])
	}
	if err := database.RecordAppActivity(ctx, owner.ID, "terminal"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if err := database.RecordAppActivity(ctx, owner.ID, "browser"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = database.HomeDashboard(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.RecentApps) < 2 || snapshot.RecentApps[0] != "browser" || snapshot.RecentApps[1] != "terminal" {
		t.Fatalf("recent apps = %#v", snapshot.RecentApps)
	}

	outsider, err := database.CreateUser("Home Outsider", "home-outsider@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.HomeDashboard(ctx, outsider.ID, space.ID); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("outsider dashboard error = %v, want ErrSpaceForbidden", err)
	}
	if err := database.RecordAppActivity(ctx, outsider.ID, "files"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = database.HomeDashboard(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, appID := range snapshot.RecentApps {
		if appID == "files" {
			t.Fatalf("owner dashboard leaked outsider app activity: %#v", snapshot.RecentApps)
		}
	}
}
