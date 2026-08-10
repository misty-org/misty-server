package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	agent "github.com/kannachi323/misty/server/internal/agents"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestingDecodeAIJSONWithLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return errInvalidJSON
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errInvalidJSON
	}
	return nil
}

func TestingWriteAIError(w http.ResponseWriter, err error) {
	var exhausted agent.HostedAILimitReachedError
	switch {
	case errors.Is(err, context.Canceled):
		writeJSON(w, 499, map[string]any{"code": "request_canceled", "message": "Agent request canceled."})
	case errors.As(err, &exhausted):
		writeJSON(w, http.StatusPaymentRequired, map[string]any{
			"code": "hosted_ai_limit_reached", "message": "Your weekly AI agent usage is fully used.",
			"reset_at": exhausted.ResetAt, "upgrade_available": true,
		})
	case errors.Is(err, agent.ErrSessionNotFound):
		http.Error(w, "session not found", http.StatusNotFound)
	case errors.Is(err, agent.ErrModelUnavailable):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "agent_model_unavailable", "message": "The selected model is unavailable. Choose another model or Automatic."})
	case errors.Is(err, db.ErrAgentToolboxActionInProgress):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "agent_action_in_progress", "message": "This Agent action is already being recorded. Retry shortly."})
	case isAIInvalidRequest(err):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func writeAISessionAccessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agent.ErrPersistedSessionNotFound):
		http.Error(w, "session not found", http.StatusNotFound)
	case errors.Is(err, db.ErrPersonalAgentNotFound), errors.Is(err, db.ErrSpaceForbidden), errors.Is(err, db.ErrLibraryForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "forbidden"})
	default:
		writeSpaceError(w, err)
	}
}

func isAIInvalidRequest(err error) bool {
	var invalid agent.ErrInvalidRequest
	return errors.As(err, &invalid)
}
