package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/misty-org/misty-server/internal/platform/db"
)

func writeDeviceResult(w http.ResponseWriter, value any, err error, status int) {
	if err != nil {
		writeDeviceError(w, err)
		return
	}
	writeJSON(w, status, value)
}

func writeDeviceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNoDevicesAllowed):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "no_devices_allowed", "message": "This account can't add devices yet."})
	case errors.Is(err, db.ErrDeviceLimit):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "device_limit_reached", "message": "This account has all the devices its plan allows. Remove one in Settings › Devices, then add this one."})
	case errors.Is(err, db.ErrDeviceIdentityConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "device_identity_conflict", "message": "This device endpoint is already registered with a different signing key. Restore the original device identity or set up a new device identity before trying again."})
	case errors.Is(err, db.ErrLibraryForbidden), errors.Is(err, db.ErrSpaceForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied", "message": "Your current Space access does not allow that action."})
	case errors.Is(err, db.ErrDeviceNotFound), errors.Is(err, db.ErrSpaceNotFound), errors.Is(err, db.ErrLibraryNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "The selected item is no longer available in this Space."})
	case errors.Is(err, db.ErrDeviceKeyRevoked):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "device_removed", "message": "This device was removed from your account."})
	case errors.Is(err, db.ErrDeviceNotAdmitted):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "device_not_added", "message": "Add this device to your account first."})
	case errors.Is(err, db.ErrDevicePolicyStale):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "device_policy_stale", "message": "A newer setting already applies."})
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
