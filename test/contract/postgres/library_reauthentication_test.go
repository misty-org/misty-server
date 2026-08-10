package db

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestLibraryReauthenticationGrantIsScopedAndExpires(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Sensitive Owner", "sensitive-owner@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Sensitive files").ID
	if valid, err := database.VerifyUserPassword(ctx, owner.ID, "password123"); err != nil || !valid {
		t.Fatalf("VerifyUserPassword(valid) = %v, %v", valid, err)
	}
	if valid, err := database.VerifyUserPassword(ctx, owner.ID, "wrong-password"); err != nil || valid {
		t.Fatalf("VerifyUserPassword(invalid) = %v, %v", valid, err)
	}
	expires, err := database.CreateLibraryReauthenticationGrant(ctx, owner.ID, spaceID, "hidden", "hashed-token", 5*time.Minute)
	if err != nil || expires.Before(time.Now().Add(4*time.Minute)) {
		t.Fatalf("CreateLibraryReauthenticationGrant() = %v, %v", expires, err)
	}
	if err := database.ValidateLibraryReauthenticationGrant(ctx, owner.ID, spaceID, "hidden", "hashed-token"); err != nil {
		t.Fatalf("ValidateLibraryReauthenticationGrant() = %v", err)
	}
	if err := database.ValidateLibraryReauthenticationGrant(ctx, owner.ID, spaceID, "recently_deleted", "hashed-token"); !errors.Is(err, ErrLibraryReauthentication) {
		t.Fatalf("wrong-scope validation = %v", err)
	}
}

func TestSensitiveLibraryItemScope(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Sensitive Item", "sensitive-item@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Sensitive items").ID
	item := createPeopleTestImage(t, database, owner.ID, spaceID, "private.jpg", "9")
	scope, err := database.SensitiveLibraryItemScope(ctx, owner.ID, spaceID, item.ID)
	if err != nil || scope != "" {
		t.Fatalf("normal scope = %q, %v", scope, err)
	}
	item, err = database.UpdateLibraryItem(ctx, owner.ID, spaceID, item.ID, item.Version, item.DisplayName, item.Caption, item.Tags, item.Favorite, true)
	if err != nil {
		t.Fatal(err)
	}
	scope, err = database.SensitiveLibraryItemScope(ctx, owner.ID, spaceID, item.ID)
	if err != nil || scope != "hidden" {
		t.Fatalf("hidden scope = %q, %v", scope, err)
	}
	item, err = database.TrashLibraryItem(ctx, owner.ID, spaceID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	scope, err = database.SensitiveLibraryItemScope(ctx, owner.ID, spaceID, item.ID)
	if err != nil || scope != "recently_deleted" {
		t.Fatalf("deleted scope = %q, %v", scope, err)
	}
}
