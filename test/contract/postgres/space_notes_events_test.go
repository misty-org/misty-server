package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// noteEventTypesFor replays a user's visible events and returns the note event
// types that reached them for one note.
func noteEventTypesFor(t *testing.T, database *Database, ctx context.Context, userID, noteID string) []string {
	t.Helper()
	events, _, err := database.SpaceEventsAfter(ctx, userID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	types := []string{}
	for _, event := range events {
		if event.EntityID == noteID && strings.HasPrefix(event.EventType, "note.") {
			types = append(types, event.EventType)
		}
	}
	return types
}

// Notes are Space documents, so their events reach every current member.
func TestNoteEventsReachEverySpaceMember(t *testing.T) {
	fixture := newNoteFixture(t, "note-event-shared")

	for name, userID := range map[string]string{
		"creator": fixture.creator, "other member": fixture.member, "space owner": fixture.owner,
	} {
		if got := noteEventTypesFor(t, fixture.database, fixture.ctx, userID, fixture.note.ID); len(got) == 0 {
			t.Fatalf("%s received no note events", name)
		}
	}
}

// Live delivery uses EventByIDForUser, replay uses SpaceEventsAfter. Both must
// apply the same rule, or one path would leak what the other hides.
func TestNoteEventVisibilityMatchesOnLiveDeliveryAndReplay(t *testing.T) {
	fixture := newNoteFixture(t, "note-event-paths")
	outsider, err := fixture.database.CreateUser("Event Outsider", "note-event-outsider@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	events, _, err := fixture.database.SpaceEventsAfter(fixture.ctx, fixture.creator, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	var noteEventID int64
	for _, event := range events {
		if event.EntityID == fixture.note.ID {
			noteEventID = event.ID
			break
		}
	}
	if noteEventID == 0 {
		t.Fatal("no note event was recorded for the creator")
	}

	for name, userID := range map[string]string{"creator": fixture.creator, "member": fixture.member} {
		if _, err := fixture.database.EventByIDForUser(fixture.ctx, userID, noteEventID); err != nil {
			t.Fatalf("%s live delivery = %v, want the event", name, err)
		}
	}
	// Someone outside the Space gets nothing through either path.
	if _, err := fixture.database.EventByIDForUser(fixture.ctx, outsider.ID, noteEventID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("outsider live delivery = %v, want ErrSpaceNotFound", err)
	}
	if got := noteEventTypesFor(t, fixture.database, fixture.ctx, outsider.ID, fixture.note.ID); len(got) != 0 {
		t.Fatalf("outsider replayed note events %v, want none", got)
	}
}

// Visibility is resolved from membership as it stands at delivery time, not as
// it stood when the event was written.
func TestFormerMemberLosesAccessToPastNoteEvents(t *testing.T) {
	fixture := newNoteFixture(t, "note-event-former")

	if got := noteEventTypesFor(t, fixture.database, fixture.ctx, fixture.member, fixture.note.ID); len(got) == 0 {
		t.Fatal("test precondition failed: member saw no events while joined")
	}

	if err := fixture.database.LeaveSpace(fixture.ctx, fixture.member, fixture.spaceID); err != nil {
		t.Fatal(err)
	}

	if got := noteEventTypesFor(t, fixture.database, fixture.ctx, fixture.member, fixture.note.ID); len(got) != 0 {
		t.Fatalf("former member replayed note events %v, want none", got)
	}
}

// Space events are stored durably and replayed, so note content must never
// ride along in one.
func TestNoteEventPayloadsCarryNoContent(t *testing.T) {
	fixture := newNoteFixture(t, "note-event-payload")

	events, _, err := fixture.database.SpaceEventsAfter(fixture.ctx, fixture.creator, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.EntityID != fixture.note.ID {
			continue
		}
		found = true
		payload := string(event.Payload)
		if strings.Contains(payload, "Beta launch checklist") {
			t.Fatalf("note event payload leaked the title: %s", payload)
		}
		if !strings.Contains(payload, fixture.note.ID) {
			t.Fatalf("note event payload is missing its note id: %s", payload)
		}
	}
	if !found {
		t.Fatal("no note events were recorded")
	}
}
