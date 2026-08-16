package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestLibraryProviderImportPersistsProvenanceAndEnforcesImportPermission(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Provider Import Owner", "provider-import-owner@example.com", "password123")
	member, _ := database.CreateUser("Provider Import Member", "provider-import-member@example.com", "password123")
	space := createTestSpace(t, database, ctx, owner.ID, "Provider Imports")
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}

	digest := strings.Repeat("c", 64)
	upload, err := database.CreateLibraryUpload(ctx, owner.ID, space.ID, "library", "plan.pdf",
		"application/pdf", 32, digest, "library/provider-import", "provider-import-token", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetLibraryUploadState(ctx, owner.ID, space.ID, upload.ID, "provider-import-token", "initiated", "uploaded_unverified"); err != nil {
		t.Fatal(err)
	}
	completed, err := database.CompleteLibraryUpload(ctx, owner.ID, space.ID, upload.ID,
		"provider-import-token", 32, digest, "application/pdf", nil)
	if err != nil || completed.Item == nil {
		t.Fatalf("CompleteLibraryUpload() = %#v, %v", completed, err)
	}
	updated, err := database.SetLibraryImportProvenance(ctx, owner.ID, space.ID, completed.Item.ID, map[string]any{
		"provider": "drive", "remote_name": "Work", "remote_path": "Documents/plan.pdf",
		"connection_id": "cloud-1", "connection_source": "connected_account",
	})
	if err != nil {
		t.Fatal(err)
	}
	var contributor map[string]any
	if json.Unmarshal(updated.ContributorInformation, &contributor) != nil {
		t.Fatalf("invalid contributor information: %s", updated.ContributorInformation)
	}
	source, _ := contributor["import_source"].(map[string]any)
	if source["connection_source"] != "connected_account" || source["remote_path"] != "Documents/plan.pdf" {
		t.Fatalf("provider provenance = %#v", contributor)
	}

	if err := database.SetSpaceMemberPermission(ctx, owner.ID, space.ID, member.ID, PermissionLibraryImport, "deny"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetLibraryImportProvenance(ctx, member.ID, space.ID, completed.Item.ID,
		map[string]any{"provider": "drive"}); !errors.Is(err, ErrSpaceForbidden) && !errors.Is(err, ErrLibraryForbidden) {
		t.Fatalf("denied member provenance error = %v, want forbidden", err)
	}
}
