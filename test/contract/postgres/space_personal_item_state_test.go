package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestSpacePersonalItemsKeepFavoritesIndependentFromVisits(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Personal state owner", "personal-items-owner@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other", "personal-items-other@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	// Seed only membership infrastructure: this contract exercises personal
	// metadata, independently of Space creation's billing integrations.
	spaceID := "space_personal_items_test"
	err = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES('sd_personal_items_test','space',$1,$2)`, owner.ID, spaceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,'Personal items','sd_personal_items_test')`, spaceID, owner.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'owner')`, spaceID, owner.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	favorite := true
	first, err := database.UpdateSpacePersonalItem(ctx, owner.ID, spaceID, "note:one", &favorite, false)
	if err != nil || !first.Favorite || first.OpenedAt != nil {
		t.Fatal(first, err)
	}
	visit, err := database.UpdateSpacePersonalItem(ctx, owner.ID, spaceID, "note:one", nil, true)
	if err != nil || !visit.Favorite || visit.OpenedAt == nil {
		t.Fatal(visit, err)
	}
	favorite = false
	unstar, err := database.UpdateSpacePersonalItem(ctx, owner.ID, spaceID, "note:one", &favorite, false)
	if err != nil || unstar.Favorite || !unstar.OpenedAt.Equal(*visit.OpenedAt) {
		t.Fatal(unstar, err)
	}
	items, err := database.SpacePersonalItems(ctx, owner.ID, spaceID)
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	if _, err = database.SpacePersonalItems(ctx, other.ID, spaceID); err == nil {
		t.Fatal("nonmember read accepted")
	}
	if _, err = database.UpdateSpacePersonalItem(ctx, other.ID, spaceID, "note:one", nil, true); err == nil {
		t.Fatal("nonmember write accepted")
	}
	if _, err = database.UpdateSpacePersonalItem(ctx, owner.ID, spaceID, "settings:private", nil, true); err == nil {
		t.Fatal("invalid item accepted")
	}
	err = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')`, spaceID, other.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err = database.SpacePersonalItems(ctx, other.ID, spaceID)
	if err != nil || len(items) != 0 {
		t.Fatal("member inherited another account's personal items", items, err)
	}
	favorite = true
	if _, err = database.UpdateSpacePersonalItem(ctx, other.ID, spaceID, "note:one", &favorite, false); err != nil {
		t.Fatal(err)
	}
	items, err = database.SpacePersonalItems(ctx, owner.ID, spaceID)
	if err != nil || len(items) != 1 || items[0].Favorite {
		t.Fatal("member's star changed another account's state", items, err)
	}
}
