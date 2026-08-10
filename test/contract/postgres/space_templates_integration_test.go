package db

import (
	"context"
	"errors"
	"reflect"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestTemplateCreationIsTransactionalAndIdempotent(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser(
		"Template Owner",
		"template-owner@example.com",
		"correct horse battery staple",
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := database.CreateSpaceWithTemplate(
		ctx,
		owner.ID,
		"Invalid template",
		"does-not-exist",
		nil,
	); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("invalid template error = %v, want ErrSpaceInvalid", err)
	}
	if _, err := database.CreateSpaceWithTemplate(
		ctx,
		owner.ID,
		"Invalid provider",
		"blank",
		[]string{"github"},
	); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("invalid provider error = %v, want ErrSpaceInvalid", err)
	}
	if spaces, err := database.ListSpaces(ctx, owner.ID); err != nil || len(standardSpaces(spaces)) != 1 {
		t.Fatalf("invalid creation left Spaces = %#v, %v", spaces, err)
	}

	first, err := database.CreateSpaceWithTemplateIdempotent(
		ctx,
		owner.ID,
		"Research group",
		"research",
		[]string{"notion", "google", "notion"},
		"create-research-once",
	)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := database.CreateSpaceWithTemplateIdempotent(
		ctx,
		owner.ID,
		"Research group",
		"research",
		[]string{"google", "notion"},
		"create-research-once",
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Space.ID != repeated.Space.ID {
		t.Fatalf("idempotent creation returned %q then %q", first.Space.ID, repeated.Space.ID)
	}
	if !reflect.DeepEqual(first.Setup.SelectedProviders, []string{"google", "notion"}) {
		t.Fatalf("selected providers = %#v", first.Setup.SelectedProviders)
	}
	if _, err := database.CreateSpaceWithTemplateIdempotent(
		ctx,
		owner.ID,
		"Different request",
		"blank",
		nil,
		"create-research-once",
	); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("reused key with different body error = %v, want ErrSpaceConflict", err)
	}

	var taskCount, noteCount, albumCount, bootstrapCount int
	if err := database.Conn.QueryRowContext(
		ctx,
		`SELECT
			(SELECT COUNT(*) FROM space_tasks WHERE space_id=$1),
			(SELECT COUNT(*) FROM space_notes WHERE space_id=$1),
			(SELECT COUNT(*) FROM space_albums WHERE space_id=$1),
			(SELECT COUNT(*) FROM space_note_control_outbox o
			 JOIN space_notes n ON n.id=o.note_id
			 WHERE n.space_id=$1 AND o.command='bootstrap')`,
		first.Space.ID,
	).Scan(&taskCount, &noteCount, &albumCount, &bootstrapCount); err != nil {
		t.Fatal(err)
	}
	if taskCount != 3 || noteCount != 1 || albumCount != 3 || bootstrapCount != 1 {
		t.Fatalf(
			"seed counts tasks=%d notes=%d albums=%d bootstraps=%d",
			taskCount,
			noteCount,
			albumCount,
			bootstrapCount,
		)
	}
}
