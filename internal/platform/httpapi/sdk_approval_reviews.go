package api

import (
	"github.com/kannachi323/misty/server/internal/browseractions"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

type sdkApprovalReview struct {
	Prepared    *browseractions.Prepared `json:"prepared,omitempty"`
	Execution   cap.Execution            `json:"execution"`
	Target      cap.Target               `json:"target"`
	Effects     cap.Effects              `json:"effects"`
	Description string                   `json:"description"`
}

// SDKCapabilityApprovalReview exposes the exact proposed action only to the
// authenticated user's trusted controls. Provider text remains untrusted data.
