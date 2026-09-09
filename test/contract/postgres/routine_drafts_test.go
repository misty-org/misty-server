package db

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func routineDraftFixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"protocol": 1, "name": name, "trigger": map[string]any{"kind": "manual"}, "budget": map[string]any{}, "steps": []any{map[string]any{"id": "read", "label": "Read habits", "kind": "capability", "action": map[string]any{"capability": "habits.list", "capabilityVersion": 1, "providerId": "example.habits/backend", "providerVersion": 1, "targetId": "10000000-0000-4000-8000-000000000001", "targetRevision": 1}, "input": map[string]any{"kind": "literal", "value": map[string]any{}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestRoutineDraftVersionsRetriesAndOwnership(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Routine owner", "routine-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other routine owner", "routine-other@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	first := routineDraftFixture(t, "Daily habits")
	result, err := database.SaveRoutineDraft(ctx, owner.ID, id, 0, first)
	if err != nil || result.Version != 1 || result.State != "draft" {
		t.Fatalf("save: %#v %v", result, err)
	}
	replay, err := database.SaveRoutineDraft(ctx, owner.ID, id, 0, first)
	if err != nil || replay.Version != 1 || !replay.CreatedAt.Equal(result.CreatedAt) {
		t.Fatalf("lost-response replay: %#v %v", replay, err)
	}
	second := routineDraftFixture(t, "Evening habits")
	if _, err := database.SaveRoutineDraft(ctx, owner.ID, id, 0, second); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("changed replay: %v", err)
	}
	result, err = database.SaveRoutineDraft(ctx, owner.ID, id, 1, second)
	if err != nil || result.Version != 2 {
		t.Fatalf("new version: %#v %v", result, err)
	}
	old, err := database.RoutineDraft(ctx, owner.ID, id, 1)
	if err != nil || old.CurrentVersion != 2 || !cap.RoutineDefinitionsEqual(old.Definition, first) {
		t.Fatalf("immutable old version: %#v %v", old, err)
	}
	if _, err := database.SaveRoutineDraft(ctx, owner.ID, id, 0, first); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("stale edit: %v", err)
	}
	if _, err := database.RoutineDraft(ctx, other.ID, id, 0); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("cross-owner read: %v", err)
	}
	appctx := WithAppExecutionAuthority(ctx, AppRuntimeSession{UserID: owner.ID, AppID: "example.habits"})
	if _, err := database.SaveRoutineDraft(appctx, owner.ID, id, 2, first); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("app authoring: %v", err)
	}
	if _, err := database.RoutineDraft(appctx, owner.ID, id, 0); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("app private read: %v", err)
	}
	if _, err := database.RoutineDrafts(appctx, owner.ID, "", "", 50); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("app private inventory: %v", err)
	}
	page, err := database.RoutineDrafts(ctx, owner.ID, "", "", 50)
	if err != nil || len(page.Routines) != 1 || page.Routines[0].Version != 2 {
		t.Fatalf("inventory: %#v %v", page, err)
	}
}
func TestRoutineDraftConcurrentSaveAndPagination(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Concurrent routines", "routine-workers@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	raw := routineDraftFixture(t, "Daily habits")
	var workers sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := database.SaveRoutineDraft(ctx, owner.ID, id, 0, raw)
			if err == nil && result.Version != 1 {
				err = errors.New("duplicate save advanced version")
			}
			errs <- err
		}()
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.SaveRoutineDraft(ctx, owner.ID, uuid.NewString(), 0, raw); err != nil {
		t.Fatal(err)
	}
	first, err := database.RoutineDrafts(ctx, owner.ID, "", "", 1)
	if err != nil || len(first.Routines) != 1 || first.NextCursor == "" {
		t.Fatalf("first page: %#v %v", first, err)
	}
	second, err := database.RoutineDrafts(ctx, owner.ID, "", first.NextCursor, 1)
	if err != nil || len(second.Routines) != 1 || second.NextCursor != "" || first.Routines[0].RoutineID == second.Routines[0].RoutineID {
		t.Fatalf("second page: %#v %v", second, err)
	}
}
