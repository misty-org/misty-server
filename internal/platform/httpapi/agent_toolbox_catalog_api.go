package api

import (
	"strings"

	"github.com/kannachi323/misty/server/internal/agenttools"
)

type agentToolboxAvailabilityReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type agentToolboxCatalogItem struct {
	Name               string                           `json:"name"`
	Description        string                           `json:"description"`
	Risk               string                           `json:"risk"`
	Approval           agenttools.ApprovalPolicy        `json:"approval"`
	Locality           agenttools.Locality              `json:"locality"`
	Idempotent         bool                             `json:"idempotent"`
	AuditEvent         string                           `json:"audit_event,omitempty"`
	RequiredPermission string                           `json:"required_permission,omitempty"`
	Granted            bool                             `json:"granted"`
	Available          bool                             `json:"available"`
	Reasons            []agentToolboxAvailabilityReason `json:"reasons"`
}

func providerFromToolName(name string) string {
	parts := strings.Split(name, ".")
	if len(parts) == 3 && parts[0] == "provider" {
		return parts[1]
	}
	return ""
}
