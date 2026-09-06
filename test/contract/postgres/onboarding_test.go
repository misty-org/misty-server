package db

import (
	"context"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestFinishOnboardingCreatesDefaultSpaceAndAppsAtomically(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("New account", "new-onboarding@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	selection := []AppInstallSpec{
		{ID: "chat", Version: "1.0.0", PermissionVersion: 1, Scopes: []string{"spaces.read", "messages.read", "messages.write"}},
		{ID: "planner", Version: "1.0.0", PermissionVersion: 1, Scopes: []string{"spaces.read", "tasks.read", "tasks.write"}},
	}

	completion, err := database.FinishOnboarding(ctx, user.ID, "Personal projects", selection)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Space == nil || !completion.Space.IsDefault || completion.Space.OwnerUserID != user.ID {
		t.Fatalf("onboarding Space = %#v", completion.Space)
	}
	if len(completion.Apps) != 2 || completion.Apps[0].State != "installed" || !completion.Apps[0].Pinned {
		t.Fatalf("onboarding apps = %#v", completion.Apps)
	}

	replayed, err := database.FinishOnboarding(ctx, user.ID, "Personal projects", selection)
	if err != nil || replayed.Space.ID != completion.Space.ID {
		t.Fatalf("idempotent replay = %#v, %v", replayed, err)
	}
	spaces, err := database.ListSpaces(ctx, user.ID)
	if err != nil || len(spaces) != 1 {
		t.Fatalf("ListSpaces() = %#v, %v", spaces, err)
	}

	if _, err := database.FinishOnboarding(ctx, user.ID, "Different name", selection); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("changed onboarding replay = %v, want ErrSpaceConflict", err)
	}
}

func TestFinishOnboardingAllowsNoStarterApps(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser("Minimal account", "minimal-onboarding@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	completion, err := database.FinishOnboarding(context.Background(), user.ID, "Home", nil)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Space == nil || !completion.Space.IsDefault || len(completion.Apps) != 0 {
		t.Fatalf("completion = %#v", completion)
	}
}
