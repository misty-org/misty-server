package workflow

import (
)






type JSONSchema map[string]any

// ContentRef is the only content identity accepted by the universal reader.
// Provider-specific credentials and raw local paths are deliberately absent.
type ContentRef struct {
	SourceKind      string `json:"sourceKind"`
	ProviderID      string `json:"providerId"`
	ResourceID      string `json:"resourceId"`
	Version         string `json:"version,omitempty"`
	Fingerprint     string `json:"fingerprint,omitempty"`
	DisplayName     string `json:"displayName"`
	MIMEType        string `json:"mimeType,omitempty"`
	Locator         string `json:"locator,omitempty"`
	PermissionScope string `json:"permissionScope"`
}












