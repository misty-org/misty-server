package api

import (
	"errors"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

// transferPurposeEnabled gates the routes that move or finalize bytes for an
// upload that already exists.
//
// Unlike initiation, these cannot be used to obtain access: the upload row was
// created by an authorized route, carries its own note binding, and is proven
// by the upload token. So note_attachment is allowed here even though the
// generic initiation endpoint rejects it.
func (s *SpaceLibraryService) transferPurposeEnabled(purpose UploadPurpose) bool {
	if purpose == UploadPurposeNoteAttachment {
		return s.NoteAssetsEnabled
	}
	if purpose == UploadPurposeDrawingAsset {
		return s.DrawingAssetsEnabled
	}
	return s.UploadPurposeEnabled(purpose)
}

func IsJournalAssetPurpose(purpose UploadPurpose) bool {
	return purpose == UploadPurposeNoteAttachment ||
		purpose == UploadPurposeDrawingAsset
}

func NewSpaceLibraryService(database *db.Database, store LibraryObjectStore, uploadsEnabled bool, limits UploadLimits) (*SpaceLibraryService, error) {
	if database == nil || store == nil {
		return nil, errors.New("Library database and permanent object store are required")
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	service := &SpaceLibraryService{
		database: database, Store: store, UploadsEnabled: uploadsEnabled, UploadLimits: limits,
		egress: NewEgressGuard(EgressBudgetFromEnv()),
	}
	if err := service.ConfigureTransfers(TransferTTLsFromEnv()); err != nil {
		return nil, err
	}
	return service, nil
}
