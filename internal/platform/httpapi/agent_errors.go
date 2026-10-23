package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/kannachi323/misty/server/internal/billingadapter"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

func writeAgentResult(w http.ResponseWriter, value any, err error, status int) {
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, status, value)
}

func writeAgentError(w http.ResponseWriter, err error) {
	var invalidRequest agent.ErrInvalidRequest
	switch {
	case errors.Is(err, billingadapter.ErrDenied), errors.Is(err, billingadapter.ErrUnavailable):
		writeBillingError(w, err)
	case errors.Is(err, db.ErrDeviceIdentityConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "device_identity_conflict", "message": "This device endpoint is already registered with a different signing key. Restore the original device identity or set up a new device identity before trying again."})
	case errors.Is(err, db.ErrAgentModelTurnLimit):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "agent_model_turn_limit", "message": "This run reached its model-turn limit. Review its completed work before starting another request."})
	case errors.Is(err, db.ErrAgentExecutionTimeLimit):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "agent_execution_time_limit", "message": "This run used its execution-time allowance. Review its completed work before starting another request."})
	case errors.As(err, &invalidRequest):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "invalid_tool_input", "message": invalidRequest.Error()})
	case errors.Is(err, db.ErrLibraryForbidden), errors.Is(err, db.ErrSpaceForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied", "message": "Your current Space access does not allow that action."})
	case errors.Is(err, db.ErrDeviceNotFound), errors.Is(err, db.ErrAgentJobNotFound), errors.Is(err, db.ErrAgentNotFound), errors.Is(err, db.ErrPersonalAgentNotFound), errors.Is(err, db.ErrSpaceNotFound), errors.Is(err, db.ErrLibraryNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "The selected item is no longer available in this Space."})
	case errors.Is(err, db.ErrDeviceKeyRevoked):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "device_removed", "message": "This device was removed from your account."})
	case errors.Is(err, db.ErrDeviceNotAdmitted):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "device_not_added", "message": "Add this device to your account first."})
	case errors.Is(err, db.ErrDeviceListConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "device_list_conflict", "message": "Your devices changed. Misty will try again."})
	case errors.Is(err, db.ErrDeviceVaultMissing):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "sync_setup_required", "message": "Set up sync to add devices."})
	case errors.Is(err, db.ErrDeviceAdmissionState):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "admission_state", "message": "This approval ended. Start again from the new device."})
	case errors.Is(err, db.ErrDevicePolicyStale):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "device_policy_stale", "message": "A newer setting already applies."})
	case errors.Is(err, db.ErrPersonalAgentConflict), errors.Is(err, db.ErrLibraryConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "version_conflict", "message": "That item changed while the Agent was working. Please retry."})
	case errors.Is(err, db.ErrSpaceConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "run_conflict", "message": "The requested change conflicts with newer Space data. Please retry."})
	case errors.Is(err, db.ErrSpaceInvalid), errors.Is(err, db.ErrLibraryInvalid):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "invalid_tool_input", "message": "The requested values are not valid for this action."})
	case errors.Is(err, db.ErrPersonalStorageQuota):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "storage_limit_reached", "reason": "personal_storage_limit_reached", "message": "Your account has reached its cloud storage limit."})
	case errors.Is(err, db.ErrSpaceStorageQuota):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "storage_limit_reached", "reason": "personal_storage_limit_reached", "message": "Your account has reached its cloud storage limit."})
	case errors.Is(err, db.ErrLibraryQuota):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "storage_limit_reached", "message": "Storage quota has been reached."})
	case errors.Is(err, db.ErrPersonalAgentModel):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "agent_model_unavailable"})
	case isHostedAILimitReached(err):
		scope, _ := hostedAILimitScope(err)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "hosted_ai_limit_reached", "reason": hostedAILimitReason(scope), "message": hostedAILimitMessage(scope)})
	case errors.Is(err, db.ErrInvalidLease), errors.Is(err, db.ErrInvalidJobState):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "invalid_or_expired_lease"})
	default:
		// The caller sees a bare 500; the reason belongs in the server log.
		log.Printf("agent request failed: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func isHostedAILimitReached(err error) bool {
	_, ok := hostedAILimitScope(err)
	return ok
}

func hostedAILimitScope(err error) (string, bool) {
	var agentLimit agent.HostedAILimitReachedError
	if errors.As(err, &agentLimit) {
		return agentLimit.Scope, true
	}
	var databaseLimit db.HostedAILimitReachedError
	if errors.As(err, &databaseLimit) {
		return databaseLimit.Scope, true
	}
	return "", false
}

func hostedAILimitReason(_ string) string { return "personal_ai_limit_reached" }

func hostedAILimitMessage(_ string) string { return "Your account has used its available AI budget." }
