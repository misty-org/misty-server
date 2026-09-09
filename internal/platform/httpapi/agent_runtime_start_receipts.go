package api

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

// Runtime workers can claim only an admitted run with its pinned configuration.
// This signed service route never accepts an app bearer as start authority.
func (s *SpacesService) AgentRuntimeStartReceipt() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AdapterVersion string `json:"adapter_version"`
			CallbackURL    string `json:"callback_url"`
			ClaimToken     string `json:"claim_token"`
			RuntimeRunID   string `json:"runtime_run_id"`
		}
		if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
			return
		}
		receipt, err := s.database.AgentRuntimeStartReceipt(r.Context(), chi.URLParam(r, "runID"), body.AdapterVersion, body.CallbackURL, body.ClaimToken, body.RuntimeRunID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, receipt)
	}
}
