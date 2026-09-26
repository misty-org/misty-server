package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

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
