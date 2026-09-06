package db

import (
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestGoCalendarSchedulerAndCallbacksExcludeNativeSources(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	user, err := database.CreateUser("Calendar ownership", "calendar-owner@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, user.ID, "Calendar ownership")
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Conn.ExecContext(ctx, `INSERT INTO space_integrations(id,space_id,provider,display_name,credential_reference,connected_by_user_id)
   VALUES('ownership-integration',$1,'google','Calendar','test-only',$2)`, space.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"go", "hono"} {
		_, err = database.Conn.ExecContext(ctx, `INSERT INTO space_calendar_sources(id,space_id,integration_id,connected_by_user_id,provider,external_calendar_id,display_name,status,watch_channel_id,execution_owner)
     VALUES($1,$2,'ownership-integration',$3,'google',$4,$4,'active',$4,$4)`, "ownership-"+owner, space.ID, user.ID, owner)
		if err != nil {
			t.Fatal(err)
		}
	}
	sources, err := database.CalendarSourcesNeedingReconciliation(ctx, 100)
	if err != nil || len(sources) != 1 || sources[0].ID != "ownership-go" {
		t.Fatalf("Go sources=%#v err=%v", sources, err)
	}
	source, err := database.CalendarSourceByWatchChannel(ctx, "go")
	if err != nil || source.ID != "ownership-go" {
		t.Fatalf("Go callback=%#v err=%v", source, err)
	}
	_, err = database.CalendarSourceByWatchChannel(ctx, "hono")
	if !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("Native source exposed to Go callback: %v", err)
	}
}
