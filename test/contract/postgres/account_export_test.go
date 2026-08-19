package db

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAccountPortableExportIncludesAuthoredDataAndNoSecrets(t *testing.T) {
	fixture := newNoteFixture(t, "account-export")
	if _, err := fixture.database.CreateSpaceDrawing(
		fixture.ctx, fixture.creator, fixture.spaceID, "Exported drawing",
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.database.CreateSpaceMessage(
		fixture.ctx,
		fixture.creator,
		fixture.spaceID,
		[]MessageSpan{{Type: "text", Text: "portable message"}},
		nil,
	); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.database.CreatePersonalAgent(fixture.ctx, fixture.creator, PersonalAgent{
		Name: "Portable Agent", Instructions: "Export this private behavior.",
		ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.SetSpaceMemberPermission(fixture.ctx, fixture.owner, fixture.spaceID, fixture.creator, PermissionAgentsManage, "allow"); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("d", 64)
	upload, err := fixture.database.CreateLibraryUpload(
		fixture.ctx, fixture.creator, fixture.spaceID, "library",
		"portable-file.png", "image/png", 32, digest,
		"library/portableexport", "portable-token", time.Now().Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SetLibraryUploadState(
		fixture.ctx, fixture.creator, fixture.spaceID, upload.ID,
		"portable-token", "initiated", "uploaded_unverified",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.CompleteLibraryUpload(
		fixture.ctx, fixture.creator, fixture.spaceID, upload.ID,
		"portable-token", 32, digest, "image/png", nil,
	); err != nil {
		t.Fatal(err)
	}

	export, err := fixture.database.AccountPortableExport(
		fixture.ctx, fixture.creator,
	)
	if err != nil {
		t.Fatal(err)
	}
	if export.FormatVersion != 2 || len(export.Spaces) != 2 ||
		len(export.Journal) != 2 || len(export.Messages) != 1 ||
		len(export.Assets) != 1 || export.Assets[0].Kind != "library" ||
		len(export.Agents) != 1 || len(export.Agents[0].Versions) != 1 ||
		len(export.Agents[0].Memberships) != 0 {
		t.Fatalf("portable export = %#v", export)
	}
	raw, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(raw))
	for _, forbidden := range []string{
		"password_hash", "credential_ciphertext", "credential_nonce",
		"status_token", "object_key",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("portable export exposed %q: %s", forbidden, text)
		}
	}
}
