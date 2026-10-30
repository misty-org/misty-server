package api

import (
	"errors"
	"net/http"

	"github.com/kannachi323/misty/server/internal/byok"
)

var (
	errAIDisabled       = errors.New("AI is turned off for this account")
	errVoiceUnavailable = errors.New("voice is unavailable on this server")
)

// writeModelError answers the errors any AI request can end with before a
// model runs. It reports false for every other error.
func writeModelError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, byok.ErrModelKeyNeeded):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "model_key_needed", "message": "Add a model key in Settings → Models."})
	case errors.Is(err, errAIDisabled):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "ai_disabled", "message": "AI is turned off for this account."})
	case errors.Is(err, ErrCollaborationOff):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "collaboration_off", "message": "This server can't save notes and drawings."})
	case errors.Is(err, errVoiceUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "voice_unavailable", "message": "Voice isn't available on this server."})
	default:
		return false
	}
	return true
}
