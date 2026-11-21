package api

import (
	"errors"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/db"
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
	if purpose == UploadPurposeDrive {
		// Started, sent straight to R2 and finished by the drive's own routes.
		return false
	}
	return s.UploadPurposeEnabled(purpose)
}

func IsJournalAssetPurpose(purpose UploadPurpose) bool {
	return purpose == UploadPurposeNoteAttachment
}

// noteAssetMIMETypes are the passive raster formats a note's images may be.
var noteAssetMIMETypes = map[string]bool{
	"image/avif":               true,
	"image/bmp":                true,
	"image/gif":                true,
	"image/jpeg":               true,
	"image/png":                true,
	"image/webp":               true,
	"image/x-icon":             true,
	"image/vnd.microsoft.icon": true,
}

func SupportedNoteAssetMIME(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	return noteAssetMIMETypes[value]
}

func NewSpaceLibraryService(database *db.Database, store LibraryObjectStore, limits UploadLimits) (*SpaceLibraryService, error) {
	if database == nil || store == nil {
		return nil, errors.New("Library database and permanent object store are required")
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	service := &SpaceLibraryService{
		database: database, Store: store, UploadLimits: limits,
		egress: NewEgressGuard(EgressBudgetFromEnv()),
	}
	if err := service.ConfigureTransfers(TransferTTLsFromEnv()); err != nil {
		return nil, err
	}
	return service, nil
}
