package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSpaceDrawingAccessAndLifecycle(t *testing.T) {
	fixture := newNoteFixture(t, "drawing-lifecycle")
	drawing, err := fixture.database.CreateSpaceDrawing(
		fixture.ctx,
		fixture.creator,
		fixture.spaceID,
		"Architecture sketch",
	)
	if err != nil {
		t.Fatal(err)
	}

	for name, userID := range map[string]string{
		"creator": fixture.creator,
		"member":  fixture.member,
		"owner":   fixture.owner,
	} {
		access, accessErr := fixture.database.DrawingAccessFor(
			fixture.ctx,
			userID,
			drawing.ID,
		)
		if accessErr != nil {
			t.Fatalf("%s access: %v", name, accessErr)
		}
		if !access.CanView || !access.CanEdit {
			t.Fatalf("%s access = %#v, want view and edit", name, access)
		}
	}

	memberAccess, err := fixture.database.DrawingAccessFor(
		fixture.ctx,
		fixture.member,
		drawing.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if memberAccess.CanDelete {
		t.Fatal("ordinary member can delete a drawing")
	}
	if err := fixture.database.DeleteSpaceDrawing(
		fixture.ctx,
		fixture.member,
		drawing.ID,
	); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("member delete error = %v, want ErrSpaceNotFound", err)
	}

	renamed, err := fixture.database.RenameSpaceDrawing(
		fixture.ctx,
		fixture.member,
		drawing.ID,
		"Updated architecture",
	)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Title != "Updated architecture" || renamed.Role != DrawingRoleEditor {
		t.Fatalf("renamed drawing = %#v", renamed)
	}

	listed, err := fixture.database.AccessibleSpaceDrawings(
		fixture.ctx,
		fixture.owner,
		fixture.spaceID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != drawing.ID || !listed[0].CanDelete {
		t.Fatalf("owner list = %#v", listed)
	}

	outsider, err := fixture.database.CreateUser(
		"Drawing Outsider",
		"drawing-lifecycle-outsider@example.com",
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	outsiderAccess, err := fixture.database.DrawingAccessFor(
		fixture.ctx,
		outsider.ID,
		drawing.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if outsiderAccess.CanView || outsiderAccess.CanEdit || outsiderAccess.CanDelete {
		t.Fatalf("outsider access = %#v, want none", outsiderAccess)
	}

	if err := fixture.database.DeleteSpaceDrawing(
		fixture.ctx,
		fixture.owner,
		drawing.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SpaceDrawingByID(
		fixture.ctx,
		fixture.creator,
		drawing.ID,
	); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("read deleting drawing error = %v, want ErrSpaceNotFound", err)
	}
	events, _, err := fixture.database.SpaceEventsAfter(
		fixture.ctx,
		fixture.member,
		0,
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []string{
		"drawing.created",
		"drawing.updated",
		"drawing.deleted",
	} {
		if !containsSpaceEvent(events, eventType, drawing.ID) {
			t.Fatalf("member event stream is missing %s", eventType)
		}
	}

	commands, err := fixture.database.PendingDrawingControlCommands(fixture.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 ||
		commands[0].DrawingID != drawing.ID ||
		commands[0].Command != "purge" {
		t.Fatalf("drawing control commands = %#v", commands)
	}
	if err := fixture.database.MarkDrawingControlDelivered(
		fixture.ctx,
		commands[0].ID,
	); err != nil {
		t.Fatal(err)
	}
	purged, err := fixture.database.PurgeDeletedDrawings(fixture.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if purged != 1 {
		t.Fatalf("purged = %d, want 1", purged)
	}

	var remaining string
	err = fixture.database.Conn.QueryRow(
		`SELECT id FROM space_drawings WHERE id=$1`,
		drawing.ID,
	).Scan(&remaining)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("purged drawing query error = %v, want sql.ErrNoRows", err)
	}
}

func TestSpaceDrawingMembershipLossInvalidatesTickets(t *testing.T) {
	fixture := newNoteFixture(t, "drawing-membership-loss")
	drawing, err := fixture.database.CreateSpaceDrawing(
		fixture.ctx,
		fixture.creator,
		fixture.spaceID,
		"Membership sketch",
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := fixture.database.RemoveSpaceMember(
		fixture.ctx,
		fixture.owner,
		fixture.spaceID,
		fixture.member,
	); err != nil {
		t.Fatal(err)
	}
	access, err := fixture.database.DrawingAccessFor(
		fixture.ctx,
		fixture.member,
		drawing.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if access.CanView || access.CanEdit {
		t.Fatalf("removed member retained drawing access: %#v", access)
	}

	commands, err := fixture.database.PendingDrawingControlCommands(fixture.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 ||
		commands[0].DrawingID != drawing.ID ||
		commands[0].Command != "acl" {
		t.Fatalf("membership control commands = %#v", commands)
	}
	var payload struct {
		ACLVersion int64 `json:"acl_version"`
	}
	if err := json.Unmarshal(commands[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ACLVersion != 2 {
		t.Fatalf("acl version = %d, want 2", payload.ACLVersion)
	}
}
