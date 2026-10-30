package api

import (
	"net/http"

	"github.com/kannachi323/misty/server/internal/modelruntime"
)

// VoiceLiveCredential tells the agent runtime which key opens a live session's
// transcription stream: POST /internal/voice/live/credential {session_id,
// user}, signed with the control secret. The session must be one this server
// issued to that account and not yet expired. It answers the route JSON:
// "account" with the account's Soniox key, or "instance" for the operator's,
// or 409 model_key_needed. The response is never logged or cached.
func (s *AgentsService) VoiceLiveCredential() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
			User      string `json:"user"`
		}
		if !readAgentRuntimeRequest(s.voiceLive.runtime, w, r, &body) {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if body.SessionID == "" || body.User == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_voice_session"})
			return
		}
		if _, err := s.database.OpenVoiceLiveSession(r.Context(), body.User, body.SessionID); err != nil {
			writeAgentError(w, err)
			return
		}
		route, err := modelruntime.RouteFor(r.Context(), body.User, "soniox/live", "")
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, route)
	}
}
