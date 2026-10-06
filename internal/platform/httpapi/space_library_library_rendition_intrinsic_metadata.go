package api

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"path/filepath"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func libraryRenditionIntrinsicMetadata(source db.LibraryTransferItem) json.RawMessage {
	metadata := map[string]any{}
	_ = json.Unmarshal(source.IntrinsicMetadata, &metadata)
	metadata["byte_size"] = source.ByteSize
	metadata["server_detected_mime_type"] = source.MIMEType
	metadata["edited_from_item_id"] = source.ItemID
	var definition db.LibraryEditDefinition
	if json.Unmarshal(source.RenditionDefinition, &definition) == nil {
		width, widthOK := metadataNumber(metadata["width"])
		height, heightOK := metadataNumber(metadata["height"])
		if widthOK && heightOK {
			if definition.Crop != nil {
				width *= definition.Crop.Width
				height *= definition.Crop.Height
			}
			if definition.Rotation == 90 || definition.Rotation == 270 {
				width, height = height, width
			}
			metadata["width"], metadata["height"] = int64(math.Round(width)), int64(math.Round(height))
		}
		if definition.Trim != nil {
			metadata["duration"] = definition.Trim.End - definition.Trim.Start
		}
		metadata["edit_definition"] = definition
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func metadataNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (s *SpaceLibraryService) rejectAndDelete(ctx context.Context, upload *db.LibraryUpload, tokenHash, state, code string) {
	_ = s.database.RejectLibraryUpload(ctx, upload.UserID, upload.SpaceID, upload.ID, tokenHash, state, code)
	_ = s.TestingStore.Delete(ctx, upload.ObjectKey)
}







func sanitizeLibraryFilename(value string) string {
	value = strings.TrimSpace(filepath.Base(strings.ReplaceAll(value, "\\", "/")))
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, value)
	if len([]rune(value)) > 255 {
		value = string([]rune(value)[:255])
	}
	return value
}

func writeLibraryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrLibraryNotFound), errors.Is(err, ErrLibraryObjectNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
	case errors.Is(err, db.ErrLibraryForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "forbidden"})
	case errors.Is(err, db.ErrLibraryReauthentication):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "library_reauthentication_required"})
	case errors.Is(err, db.ErrLibraryInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
	case errors.Is(err, db.ErrPersonalStorageQuota):
		writeJSON(w, http.StatusConflict, map[string]any{"code": "owner_storage_quota_exceeded", "reason": "personal_storage_limit_reached", "owner_can_upgrade": true})
	case errors.Is(err, db.ErrSpaceStorageQuota):
		writeJSON(w, http.StatusConflict, map[string]any{"code": "owner_storage_quota_exceeded", "reason": "space_storage_limit_reached", "owner_can_upgrade": true})
	case errors.Is(err, db.ErrLibraryQuota):
		writeJSON(w, http.StatusConflict, map[string]any{"code": "owner_storage_quota_exceeded", "owner_can_upgrade": true})
	case isHostedAILimitReached(err):
		scope, _ := hostedAILimitScope(err)
		writeJSON(w, http.StatusPaymentRequired, map[string]any{"code": "hosted_ai_limit_reached", "reason": hostedAILimitReason(scope), "message": hostedAILimitMessage(scope), "upgrade_available": true})
	case errors.Is(err, db.ErrLibraryConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "version_conflict"})
	case errors.Is(err, db.ErrLibraryUploadMismatch):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "upload_verification_failed"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
	}
}
