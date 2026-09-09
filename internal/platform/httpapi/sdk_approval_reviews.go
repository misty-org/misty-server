package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/browseractions"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type sdkApprovalReview struct {
	Prepared    *browseractions.Prepared `json:"prepared,omitempty"`
	Execution   cap.Execution            `json:"execution"`
	Target      cap.Target               `json:"target"`
	Effects     cap.Effects              `json:"effects"`
	Description string                   `json:"description"`
}

func (s *SpacesService) protectSDKApprovalReview(execution cap.Execution, bound *db.SDKBoundCapability, browser ...*sdkBrowserExecution) (db.ProtectedSDKApproval, error) {
	var prepared *browseractions.Prepared
	if len(browser) > 0 && browser[0] != nil {
		prepared = &browser[0].Prepared
	}
	raw, err := json.Marshal(sdkApprovalReview{Prepared: prepared, Execution: execution, Target: bound.Target, Effects: bound.Definition.Effects, Description: bound.Definition.Description})
	if err != nil {
		return db.ProtectedSDKApproval{}, err
	}
	raw, err = cap.CanonicalJSON(raw)
	if err != nil {
		return db.ProtectedSDKApproval{}, err
	}
	digest := sha256.Sum256(raw)
	encrypted, err := s.protectAgentEffectResult(execution.EffectID+":approval-review", raw)
	return db.ProtectedSDKApproval{EffectID: execution.EffectID, Digest: hex.EncodeToString(digest[:]), Ciphertext: encrypted}, err
}

// SDKCapabilityApprovalReview exposes the exact proposed action only to the
// authenticated user's trusted controls. Provider text remains untrusted data.
func (s *SpacesService) SDKCapabilityApprovalReview() http.HandlerFunc {
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
		if approval.ToolName == "browser.click" || approval.ToolName == "browser.interact" {
			var review browserApprovalReview
			if json.Unmarshal(raw, &review) != nil || review.Kind != "browser" || review.EffectID != protected.EffectID || review.RunID != approval.RunID || review.CallID != approval.ToolCallID || review.Operation != approval.ToolName {
				writeSDKError(w, db.ErrSpaceConflict)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, map[string]any{"approval": approval, "review": review})
			return
		}
		var review sdkApprovalReview
		if json.Unmarshal(raw, &review) != nil || review.Execution.EffectID != protected.EffectID {
			writeSDKError(w, db.ErrSpaceConflict)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"approval": approval, "review": review})
	}
}

func (s *SpacesService) SDKCapabilityApprovals() http.HandlerFunc {
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
		page, err := s.database.SDKPendingApprovals(r.Context(), userID, cursor, limit)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, page)
	}
}
