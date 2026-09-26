package api

import (
	"errors"
	"io"
	"net/http"

	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func decodeCapabilityRequest(w http.ResponseWriter, r *http.Request, out any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 3<<20))
	if err != nil || cap.Decode(raw, out) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_capability_request"})
		return false
	}
	return true
}
func trustedSDKUser(w http.ResponseWriter, r *http.Request, database *db.Database) (string, bool) {
	userID, ok := authenticatedUser(w, r, database)
	if !ok {
		return "", false
	}
	if db.AppAuthorityFromContext(r.Context()) != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "trusted_user_control_required"})
		return "", false
	}
	return userID, true
}

func writeSDKError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrSDKTargetClarification):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "target_clarification_required", "message": err.Error()})
	case errors.Is(err, db.ErrSpaceNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "sdk_request_not_found"})
	case errors.Is(err, db.ErrSpaceConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "sdk_request_conflict"})
	case errors.Is(err, cap.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_capability_declaration"})
	case errors.Is(err, db.ErrSDKVersionConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "sdk_version_conflict", "message": "Publish a new version for changed provider content."})
	case errors.Is(err, db.ErrSpaceForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "target_scope_forbidden"})
	case errors.Is(err, db.ErrSpaceLimit):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "target_selection_required", "message": "Choose an explicit target to narrow these results."})
	case errors.Is(err, db.ErrSDKProviderUnavailable):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "sdk_provider_unavailable"})
	default:
		writeSpaceError(w, err)
	}
}
