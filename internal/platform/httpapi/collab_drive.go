package api

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/misty-org/misty-server/internal/collab/file"
	"github.com/misty-org/misty-server/internal/platform/db"
)

func collabDriveRefused(err error) bool {
	return errors.Is(err, db.ErrDriveNotFound) || errors.Is(err, db.ErrLibraryNotFound) ||
		errors.Is(err, ErrLibraryObjectNotFound) || errors.Is(err, db.ErrDriveParentMissing) ||
		errors.Is(err, db.ErrSpaceNotFound) || errors.Is(err, db.ErrSpaceForbidden) || errors.Is(err, db.ErrLibraryForbidden)
}

func collabDriveError(err error) error {
	if err != nil && collabDriveRefused(err) {
		return errors.Join(file.ErrRefused, err)
	}
	return err
}

func (s *SpaceDriveService) CollabDrive() file.Drive {
	return file.Drive{Versions: s.collabVersions, SaveText: s.collabSaveText, KeepCopy: s.collabKeepCopy}
}

func (s *SpaceDriveService) collabVersions(ctx context.Context, spaceID, itemID, userID string) (file.Versions, error) {
	item, err := s.database.DriveItem(ctx, userID, spaceID, itemID)
	if err == nil && (item.Kind != "file" || item.Deleted) {
		err = db.ErrDriveNotFound
	}
	if err != nil {
		return file.Versions{}, collabDriveError(err)
	}
	return file.Versions{Version: item.Version, ContentVersion: item.ContentVersion, ModifiedBy: item.ModifiedBy}, nil
}

func (s *SpaceDriveService) collabSaveText(ctx context.Context, spaceID, itemID, userID, text string, base int64) (int64, error) {
	if !utf8.ValidString(text) || len(text) > fileRoomTextBodyLimit {
		return 0, db.ErrSpaceInvalid
	}
	item, err := s.saveFileRoomText(ctx, userID, spaceID, itemID, base, []byte(text))
	var stale fileRoomTextStale
	if errors.As(err, &stale) {
		return 0, &file.Stale{Version: stale.version}
	}
	if err != nil {
		return 0, collabDriveError(err)
	}
	return item.Version, nil
}

func (s *SpaceDriveService) collabKeepCopy(ctx context.Context, spaceID, itemID, userID, text string) (string, error) {
	if !utf8.ValidString(text) || len(text) > fileRoomTextBodyLimit {
		return "", db.ErrSpaceInvalid
	}
	item, err := s.keepFileRoomTextCopy(ctx, userID, spaceID, itemID, []byte(text))
	if err != nil {
		return "", collabDriveError(err)
	}
	return item.Name, nil
}
