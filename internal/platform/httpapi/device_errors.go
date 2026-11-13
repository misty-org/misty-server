package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/misty-org/misty-server/internal/platform/db"
)

func writeDeviceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrLibraryForbidden), errors.Is(err, db.ErrSpaceForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied", "message": "Your current Space access does not allow that action."})
	case errors.Is(err, db.ErrSpaceNotFound), errors.Is(err, db.ErrLibraryNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "The selected item is no longer available in this Space."})
	case errors.Is(err, db.ErrLibraryConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "version_conflict", "message": "That item changed while the Agent was working. Please retry."})
	case errors.Is(err, db.ErrSpaceConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "run_conflict", "message": "The requested change conflicts with newer Space data. Please retry."})
	case errors.Is(err, db.ErrSpaceInvalid), errors.Is(err, db.ErrLibraryInvalid):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "invalid_tool_input", "message": "The requested values are not valid for this action."})
	case errors.Is(err, db.ErrLibraryQuota):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "storage_limit_reached", "message": "Storage quota has been reached."})
	default:
		// The caller sees a bare 500; the reason belongs in the server log.
		log.Printf("agent request failed: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
