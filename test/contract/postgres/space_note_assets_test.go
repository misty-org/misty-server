package db

import (
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func noteAssetUpload(t *testing.T, fixture noteFixture, userID string, byteSize int64, suffix string) (*LibraryUpload, error) {
	t.Helper()
	return fixture.database.CreateNoteAssetUpload(fixture.ctx, userID, fixture.note.ID,
		"diagram-"+suffix+".png", "image/png", byteSize, strings.Repeat("a", 64),
		"library/noteasset"+suffix, "note-asset-token-"+suffix, time.Now().Add(time.Hour))
}

// Uploading an asset requires edit access to the parent note, which every
// current Space member has. Someone outside the Space has none.
func TestNoteAssetUploadRequiresNoteEditAccess(t *testing.T) {
	fixture := newNoteFixture(t, "note-asset-auth")

	for name, userID := range map[string]string{
		"creator": fixture.creator, "other member": fixture.member, "space owner": fixture.owner,
	} {
		if _, err := noteAssetUpload(t, fixture, userID, 1024, "ok-"+strings.ReplaceAll(name, " ", "-")); err != nil {
			t.Fatalf("%s upload = %v, want success", name, err)
		}
	}

	outsider, err := fixture.database.CreateUser("Asset Outsider", "note-asset-outsider@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := noteAssetUpload(t, fixture, outsider.ID, 1024, "denied"); !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("non-member upload = %v, want ErrLibraryNotFound", err)
	}
}

// The 15 MB note ceiling is enforced in the database, independently of the API
// service and the desktop.
func TestNoteAssetUploadEnforcesTheNoteLimit(t *testing.T) {
	fixture := newNoteFixture(t, "note-asset-limit")

	if _, err := noteAssetUpload(t, fixture, fixture.creator, DefaultNoteAttachmentMaxFileBytes, "atlimit"); err != nil {
		t.Fatalf("upload exactly at the limit = %v, want success", err)
	}
	if _, err := noteAssetUpload(t, fixture, fixture.creator, DefaultNoteAttachmentMaxFileBytes+1, "overlimit"); !errors.Is(err, ErrLibraryInvalid) {
		t.Fatalf("upload one byte over the limit = %v, want ErrLibraryInvalid", err)
	}
	// The larger Library ceiling must not apply to a note asset.
	if _, err := noteAssetUpload(t, fixture, fixture.creator, DefaultLibraryMaxFileBytes, "librarysize"); !errors.Is(err, ErrLibraryInvalid) {
		t.Fatalf("upload at the Library limit = %v, want ErrLibraryInvalid", err)
	}
}

// The upload row must record its note so finalization creates a note asset
// rather than a Library item.
func TestNoteAssetUploadRecordsItsParentNote(t *testing.T) {
	fixture := newNoteFixture(t, "note-asset-binding")

	upload, err := noteAssetUpload(t, fixture, fixture.creator, 2048, "binding")
	if err != nil {
		t.Fatal(err)
	}

	var noteID, purpose string
	if err := fixture.database.Conn.QueryRow(
		`SELECT COALESCE(note_id,''),purpose FROM space_library_uploads WHERE id=$1`, upload.ID).Scan(&noteID, &purpose); err != nil {
		t.Fatal(err)
	}
	if noteID != fixture.note.ID {
		t.Fatalf("stored note_id = %q, want %q", noteID, fixture.note.ID)
	}
	if purpose != UploadPurposeNoteAttachment {
		t.Fatalf("stored purpose = %q, want %q", purpose, UploadPurposeNoteAttachment)
	}
}

// A Library upload must never carry a note reference, and a note upload must
// never lack one. The database enforces both directions.
func TestUploadPurposeAndNoteReferenceMustAgree(t *testing.T) {
	fixture := newNoteFixture(t, "note-asset-constraint")

	_, err := fixture.database.Conn.Exec(
		`INSERT INTO space_library_uploads(id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,client_declared_mime_type,requested_byte_size,client_sha256,state,upload_token_hash,expires_at,note_id)
		 SELECT 'upload_bad_library',n.space_id,s.security_domain_id,$1,'library/badlibrary','x.png','library','image/png',10,$2,'initiated','tok',NOW()+INTERVAL '1 hour',n.id
		 FROM space_notes n JOIN spaces s ON s.id=n.space_id WHERE n.id=$3`,
		fixture.creator, strings.Repeat("a", 64), fixture.note.ID)
	if err == nil {
		t.Fatal("a library upload was allowed to reference a note")
	}

	_, err = fixture.database.Conn.Exec(
		`INSERT INTO space_library_uploads(id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,client_declared_mime_type,requested_byte_size,client_sha256,state,upload_token_hash,expires_at)
		 SELECT 'upload_bad_note',n.space_id,s.security_domain_id,$1,'library/badnote','x.png','note_attachment','image/png',10,$2,'initiated','tok',NOW()+INTERVAL '1 hour'
		 FROM space_notes n JOIN spaces s ON s.id=n.space_id WHERE n.id=$3`,
		fixture.creator, strings.Repeat("a", 64), fixture.note.ID)
	if err == nil {
		t.Fatal("a note_attachment upload was allowed without a note reference")
	}
}

// Listing and downloading follow the parent note's access, which is Space
// membership.
func TestNoteAssetVisibilityFollowsTheParentNote(t *testing.T) {
	fixture := newNoteFixture(t, "note-asset-visibility")

	for name, userID := range map[string]string{
		"creator": fixture.creator, "other member": fixture.member, "space owner": fixture.owner,
	} {
		if _, err := fixture.database.NoteAssets(fixture.ctx, userID, fixture.note.ID); err != nil {
			t.Fatalf("%s NoteAssets() = %v, want success", name, err)
		}
	}

	outsider, err := fixture.database.CreateUser("Visibility Outsider", "note-asset-vis-outsider@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.NoteAssets(fixture.ctx, outsider.ID, fixture.note.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("non-member NoteAssets() = %v, want ErrSpaceNotFound", err)
	}
	if _, err := fixture.database.NoteAssetDownload(fixture.ctx, outsider.ID, fixture.note.ID, "noteasset_x"); !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("non-member NoteAssetDownload() = %v, want ErrLibraryNotFound", err)
	}
}

// Removing an asset is idempotent, so a retried request does not error.
func TestDeleteNoteAssetIsIdempotentAndRequiresEditAccess(t *testing.T) {
	fixture := newNoteFixture(t, "note-asset-delete")

	outsider, err := fixture.database.CreateUser("Delete Outsider", "note-asset-del-outsider@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.DeleteNoteAsset(fixture.ctx, outsider.ID, fixture.note.ID, "noteasset_missing"); !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("non-member delete = %v, want ErrLibraryNotFound", err)
	}
	if err := fixture.database.DeleteNoteAsset(fixture.ctx, fixture.creator, fixture.note.ID, "noteasset_missing"); err != nil {
		t.Fatalf("creator delete of a missing asset = %v, want idempotent success", err)
	}
}
