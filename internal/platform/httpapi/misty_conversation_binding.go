package api

import (
	"errors"
	"net/http"

	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func writeMistyConversationBindingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrSpaceConflict):
		writeJSON(w, http.StatusConflict, map[string]string{
			"code":    "conversation_context_changed",
			"message": "Start a new conversation to work in a different Space.",
		})
	case errors.Is(err, agent.ErrPersistedSessionNotFound):
		http.Error(w, "conversation not found", http.StatusNotFound)
	default:
		writeSpaceError(w, err)
	}
}
