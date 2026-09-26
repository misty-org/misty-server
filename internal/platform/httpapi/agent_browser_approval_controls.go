package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"net/http"
	"strconv"
	"strings"
)

// AgentBrowserApprovalReview exposes the exact proposed action only to the
// authenticated user's trusted controls. Provider text remains untrusted data.
func (s *SpacesService) AgentBrowserApprovalReview() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, s.database)
		if !ok {
			return
		}
		id := chi.URLParam(r, "approvalID")
		if !cap.ValidID(strings.TrimPrefix(id, "approval_")) {
			writeSDKError(w, cap.ErrInvalid)
			return
		}
		approval, protected, err := s.database.SDKApprovalReview(r.Context(), userID, id)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		raw, err := s.restoreAgentEffectResult(protected.EffectID+":approval-review", protected.Ciphertext)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		digest := sha256.Sum256(raw)
		if hex.EncodeToString(digest[:]) != protected.Digest {
			writeSDKError(w, db.ErrSpaceConflict)
			return
		}
		if approval.ToolName == "browser.click" || approval.ToolName == "browser.interact" || approval.ToolName == "browser.workspace.interact" {
			var review browserApprovalReview
			if json.Unmarshal(raw, &review) != nil || review.Kind != "browser" || review.EffectID != protected.EffectID || review.RunID != approval.RunID || review.CallID != approval.ToolCallID || review.Operation != approval.ToolName {
				writeSDKError(w, db.ErrSpaceConflict)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, map[string]any{"approval": approval, "review": review})
			return
		}
		if strings.HasPrefix(approval.ToolName, "sdk.") {
			var review sdkApprovalReview
			if json.Unmarshal(raw, &review) != nil || review.Execution.EffectID != protected.EffectID {
				writeSDKError(w, db.ErrSpaceConflict)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, map[string]any{"approval": approval, "review": review})
			return
		}
		writeSDKError(w, db.ErrSpaceForbidden)
	}
}

func (s *SpacesService) AgentBrowserApprovals() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, s.database)
		if !ok {
			return
		}
		limit := 20
		if value := r.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 100 {
				writeSDKError(w, cap.ErrInvalid)
				return
			}
			limit = parsed
		}
		cursor := r.URL.Query().Get("cursor")
		if cursor != "" && !cap.ValidID(strings.TrimPrefix(cursor, "approval_")) {
			writeSDKError(w, cap.ErrInvalid)
			return
		}
		page, err := s.database.AgentPendingBrowserApprovals(r.Context(), userID, cursor, limit)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, page)
	}
}

// AgentBrowserApprovalDecision keeps invocation decisions independent of app runs.
func (s *SpacesService) AgentBrowserApprovalDecision() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, s.database)
		if !ok {
			return
		}
		runID, approvalID := chi.URLParam(r, "runID"), chi.URLParam(r, "approvalID")
		if !cap.ValidID(runID) || !cap.ValidID(approvalID) {
			writeSDKError(w, cap.ErrInvalid)
			return
		}
		approval, _, err := s.database.SDKApprovalReview(r.Context(), userID, approvalID)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		if approval.RunID != "invocation_"+strings.ToLower(runID) || (approval.ToolName != "browser.click" && approval.ToolName != "browser.interact" && approval.ToolName != "browser.workspace.interact" && !strings.HasPrefix(approval.ToolName, "sdk.")) {
			writeSDKError(w, db.ErrSpaceForbidden)
			return
		}
		var body struct {
			Approved *bool `json:"approved"`
		}
		if !decodeCapabilityRequest(w, r, &body) {
			return
		}
		if body.Approved == nil {
			writeSDKError(w, cap.ErrInvalid)
			return
		}
		if err = s.database.DecideSDKToolApproval(r.Context(), userID, approval.RunID, approvalID, *body.Approved); err != nil {
			writeSDKError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
