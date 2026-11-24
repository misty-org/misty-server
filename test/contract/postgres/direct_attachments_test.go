package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/misty-org/misty-server/internal/platform/db"
)

func TestDirectMessageFilesAreReadableByTheDMAlone(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	ana, err := database.CreateUserWithUsername("dmfile_ana", "dmfile-ana@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	ben, err := database.CreateUserWithUsername("dmfile_ben", "dmfile-ben@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	cam, err := database.CreateUserWithUsername("dmfile_cam", "dmfile-cam@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	makeFriends(t, database, ctx, ana, ben)
	dm, err := database.OpenDirect(ctx, ana.ID, ben.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RequireDirectMember(ctx, cam.ID, dm.ID); !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("RequireDirectMember(stranger) error = %v, want ErrLibraryNotFound", err)
	}
	if direct, err := database.IsDirectSpace(ctx, dm.ID); err != nil || !direct {
		t.Fatalf("IsDirectSpace(dm) = %v, %v; want true", direct, err)
	}

	digest := strings.Repeat("c", 64)
	expires := time.Now().Add(time.Hour)
	if _, err := database.CreateLibraryUpload(ctx, ana.ID, dm.ID, UploadPurposeNoteAttachment, "note.png", "image/png", 64, digest, "library/dmnote", "note-token", expires); !errors.Is(err, ErrLibraryInvalid) {
		t.Fatalf("note upload into a DM error = %v, want ErrLibraryInvalid", err)
	}
	if _, err := database.CreateLibraryUpload(ctx, cam.ID, dm.ID, UploadPurposeChatAttachment, "intruder.png", "image/png", 64, digest, "library/dmintruder", "intruder-token", expires); !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("stranger's upload into a DM error = %v, want ErrLibraryNotFound", err)
	}

	upload, err := database.CreateLibraryUpload(ctx, ana.ID, dm.ID, UploadPurposeChatAttachment, "photo.png", "image/png", 64, digest, "library/dmphoto", "photo-token", expires)
	if err != nil {
		t.Fatalf("upload into the DM error = %v", err)
	}
	if _, err := database.SetLibraryUploadState(ctx, ana.ID, dm.ID, upload.ID, "photo-token", "initiated", "uploaded_unverified"); err != nil {
		t.Fatal(err)
	}
	completed, err := database.CompleteLibraryUpload(ctx, ana.ID, dm.ID, upload.ID, "photo-token", 64, digest, "image/png", nil)
	if err != nil || completed.Attachment == nil {
		t.Fatalf("CompleteLibraryUpload() = %#v, %v", completed, err)
	}
	if _, _, err := database.CreateSpaceMessageWithReferencesAndClientNonce(ctx, ana.ID, dm.ID, nil, nil, []string{completed.Attachment.ID}, "", ""); err != nil {
		t.Fatalf("sending the photo error = %v", err)
	}

	download, err := database.DirectAttachmentDownload(ctx, ben.ID, dm.ID, completed.Attachment.ID)
	if err != nil || download.ObjectKey != "library/dmphoto" {
		t.Fatalf("DirectAttachmentDownload(ben) = %#v, %v", download, err)
	}
	if _, err := database.DirectAttachmentDownload(ctx, cam.ID, dm.ID, completed.Attachment.ID); !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("DirectAttachmentDownload(stranger) error = %v, want ErrLibraryNotFound", err)
	}
	if _, err := database.MessageAttachmentDownload(ctx, ben.ID, dm.ID, completed.Attachment.ID); !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("Space download of a DM file error = %v, want ErrLibraryNotFound", err)
	}

	shared := createTestSpace(t, database, ctx, ana.ID, "Not a DM")
	if direct, err := database.IsDirectSpace(ctx, shared.ID); err != nil || direct {
		t.Fatalf("IsDirectSpace(Space) = %v, %v; want false", direct, err)
	}
	if _, err := database.DirectAttachmentDownload(ctx, ana.ID, shared.ID, completed.Attachment.ID); !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("DM download from a Space error = %v, want ErrLibraryNotFound", err)
	}
}
