package db

import (
	"context"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// taskLWWFixture creates one owner, one Space, and one active task.
func taskLWWFixture(t *testing.T, emailPrefix string) (*Database, context.Context, string, *SpaceTask) {
	t.Helper()
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Task Owner", emailPrefix+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Task Space")
	if err != nil {
		t.Fatal(err)
	}
	task, err := database.CreateSpaceTask(ctx, owner.ID, SpaceTask{
		SpaceID: space.ID, Title: "Original title", Status: "todo",
	})
	if err != nil {
		t.Fatal(err)
	}
	return database, ctx, owner.ID, task
}

// A second writer holding a stale version used to get a 409. Active tasks are
// now last-write-wins: the later server-received write is applied.
func TestStaleActiveTaskWriteWinsInsteadOfConflicting(t *testing.T) {
	database, ctx, ownerID, task := taskLWWFixture(t, "task-lww-active")

	first, err := database.UpdateSpaceTask(ctx, ownerID, SpaceTask{
		ID: task.ID, SpaceID: task.SpaceID, Title: "First writer", Status: "todo", Version: task.Version,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The second writer still submits the original (now stale) version.
	second, err := database.UpdateSpaceTask(ctx, ownerID, SpaceTask{
		ID: task.ID, SpaceID: task.SpaceID, Title: "Second writer", Status: "todo", Version: task.Version,
	})
	if err != nil {
		t.Fatalf("stale active write returned %v, want last-write-wins success", err)
	}

	if second.Title != "Second writer" {
		t.Fatalf("Title = %q, want the last received write to win", second.Title)
	}
	if second.Version <= first.Version {
		t.Fatalf("Version = %d, want greater than the previous write's %d", second.Version, first.Version)
	}
}

// Archived tasks are tombstones. A stale write must not resurrect one or clear
// archived_at.
func TestStaleWriteCannotResurrectArchivedTask(t *testing.T) {
	database, ctx, ownerID, task := taskLWWFixture(t, "task-lww-archived")

	archived, err := database.ArchiveSpaceTask(ctx, ownerID, task.SpaceID, task.ID, task.Version)
	if err != nil {
		t.Fatal(err)
	}
	if archived.ArchivedAt == nil {
		t.Fatal("ArchiveSpaceTask() did not set archived_at")
	}

	_, updateErr := database.UpdateSpaceTask(ctx, ownerID, SpaceTask{
		ID: task.ID, SpaceID: task.SpaceID, Title: "Resurrected", Status: "todo", Version: task.Version,
	})
	if !errors.Is(updateErr, ErrSpaceNotFound) {
		t.Fatalf("UpdateSpaceTask() on a tombstone = %v, want ErrSpaceNotFound", updateErr)
	}

	_, moveErr := database.MoveSpaceTask(ctx, ownerID, task.SpaceID, task.ID, SpaceTaskMove{
		Status: "in_progress", Version: task.Version,
	})
	if !errors.Is(moveErr, ErrSpaceNotFound) {
		t.Fatalf("MoveSpaceTask() on a tombstone = %v, want ErrSpaceNotFound", moveErr)
	}

	// The tombstone must be untouched by either rejected write.
	after, err := database.ArchiveSpaceTask(ctx, ownerID, task.SpaceID, task.ID, archived.Version)
	if err != nil {
		t.Fatalf("re-archiving = %v, want idempotent success", err)
	}
	if after.ArchivedAt == nil {
		t.Fatal("archived_at was cleared by a stale write")
	}
	if after.Title != "Original title" {
		t.Fatalf("Title = %q, want the pre-archive value", after.Title)
	}
}

// Re-archiving is idempotent so a retried request after a client timeout does
// not surface as a conflict.
func TestArchiveIsIdempotent(t *testing.T) {
	database, ctx, ownerID, task := taskLWWFixture(t, "task-lww-idempotent")

	first, err := database.ArchiveSpaceTask(ctx, ownerID, task.SpaceID, task.ID, task.Version)
	if err != nil {
		t.Fatal(err)
	}
	// The retry sends the same now-stale version the client had originally.
	second, err := database.ArchiveSpaceTask(ctx, ownerID, task.SpaceID, task.ID, task.Version)
	if err != nil {
		t.Fatalf("second archive = %v, want idempotent success", err)
	}

	if second.ArchivedAt == nil {
		t.Fatal("idempotent archive lost archived_at")
	}
	if second.Version != first.Version {
		t.Fatalf("Version = %d, want the first archive's %d (no second bump)", second.Version, first.Version)
	}
}

// A move with a stale version is applied rather than rejected; the later
// server-received move wins.
func TestStaleMoveWinsOnActiveTask(t *testing.T) {
	database, ctx, ownerID, task := taskLWWFixture(t, "task-lww-move")

	if _, err := database.MoveSpaceTask(ctx, ownerID, task.SpaceID, task.ID, SpaceTaskMove{
		Status: "in_progress", Version: task.Version,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := database.MoveSpaceTask(ctx, ownerID, task.SpaceID, task.ID, SpaceTaskMove{
		Status: "done", Version: task.Version,
	})
	if err != nil {
		t.Fatalf("stale move returned %v, want last-write-wins success", err)
	}
	if result.Task.Status != "done" {
		t.Fatalf("Status = %q, want done", result.Task.Status)
	}
}

// Updates against a task that never existed stay not-found.
func TestUpdateMissingTaskIsNotFound(t *testing.T) {
	database, ctx, ownerID, task := taskLWWFixture(t, "task-lww-missing")

	_, err := database.UpdateSpaceTask(ctx, ownerID, SpaceTask{
		ID: "task_00000000-0000-0000-0000-000000000000", SpaceID: task.SpaceID,
		Title: "Ghost", Status: "todo", Version: 1,
	})
	if !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("UpdateSpaceTask() on a missing task = %v, want ErrSpaceNotFound", err)
	}
}
