package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/kannachi323/misty/server/internal/billingadapter"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// VoiceLiveSettle is called by the agent runtime, signed with the control
// secret, when a live session closes. It settles the measured audio once;
// a repeated call is acknowledged without charging again.
func (s *AgentsService) VoiceLiveSettle() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
			User      string `json:"user"`
			UsedMS    int64  `json:"used_ms"`
		}
		if !readAgentRuntimeRequest(s.voiceLive.runtime, w, r, &body) {
			return
		}
		if body.SessionID == "" || body.User == "" || body.UsedMS < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_voice_settlement"})
			return
		}
		status := "settled"
		if body.UsedMS == 0 {
			status = "released"
		}
		session, err := s.database.CloseVoiceLiveSession(r.Context(), body.User, body.SessionID, body.UsedMS, status)
		if errors.Is(err, db.ErrVoiceSessionClosed) {
			writeJSON(w, http.StatusOK, map[string]any{"settled": true, "used_ms": session.UsedMS, "repeat": true})
			return
		}
		if err != nil {
			writeAgentError(w, err)
			return
		}
		var reservation billingadapter.Reservation
		if err = json.Unmarshal(session.Reservation, &reservation); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "voice_reservation_unreadable"})
			return
		}
		billing := s.database.BillingService()
		key := "voice-live:" + session.ID
		if status == "released" {
			err = billing.Complete(r.Context(), "release", &reservation, key+":release", billingadapter.Usage{}, "no_audio")
		} else {
			err = billing.Complete(r.Context(), "settle", &reservation, key+":settle", billingadapter.Usage{Provider: "soniox", Model: reservation.Admission.Usage.Model, Units: map[string]int64{"audio_ms": session.UsedMS}}, "")
		}
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"settled": true, "used_ms": session.UsedMS})
	}
}
