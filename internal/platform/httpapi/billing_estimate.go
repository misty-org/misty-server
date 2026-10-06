package api

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"io"
	"net/http"
	"strings"
)

// EstimateAIUsage forwards native draft size, never a client-selected allowance.
// It is a preview, not a reservation; execution always checks its assembled context.
func EstimateAIUsage(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := sessionUserID(r, database)
		if err != nil || user == "" {
			http.Error(w, "not authenticated", 401)
			return
		}
		var body struct {
			Text  string `json:"text"`
			Model string `json:"model"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || strings.TrimSpace(body.Text) == "" || len(body.Model) > 256 {
			writeJSON(w, 400, map[string]string{"code": "invalid_usage_estimate"})
			return
		}
		model := strings.TrimSpace(body.Model)
		if model == "" {
			model = agent.FrontierDefaultModelID()
		}
		adapter := database.BillingService().Adapter
		if !adapter.Enabled() {
			writeJSON(w, 200, map[string]any{"available": false})
			return
		}
		key := "estimate:" + uuid.NewString()
		decision, err := adapter.Do(r.Context(), "check", billingadapter.Request{Version: 1, AccountID: user, Operation: "agent.model", OperationID: key, Key: key, Usage: billingadapter.Usage{Model: model, Units: map[string]int64{"input_bytes": int64(len(body.Text)), "output_tokens": agent.MaxModelOutputTokens}, Estimated: true}})
		if err != nil && !errors.Is(err, billingadapter.ErrDenied) {
			writeBillingError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"available": true, "allowed": decision.Allowed, "usage": decision.Summary, "basis": "draft_only", "model": model})
	}
}
