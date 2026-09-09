package capabilities

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
)

var profileDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

var opaqueResourceID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

// Target is the public identity. Connection revisions and caller access ceilings
// are protected control-plane fields, not data supplied by execution requests.
type Target struct {
	ID              string          `json:"id"`
	Revision        int             `json:"revision"`
	AppID           string          `json:"appId"`
	ProviderID      string          `json:"providerId"`
	ProviderVersion int             `json:"providerVersion"`
	SpaceID         string          `json:"spaceId,omitempty"`
	Label           string          `json:"label"`
	Binding         json.RawMessage `json:"binding"`
}
type BackendBinding struct {
	Kind         string `json:"kind"`
	ConnectionID string `json:"connectionId"`
}
type TargetResolve struct {
	SpaceID    string `json:"spaceId,omitempty"`
	ProviderID string `json:"providerId,omitempty"`
	Capability string `json:"capability"`
	TargetID   string `json:"targetId,omitempty"`
	ContextID  string `json:"contextId,omitempty"`
}
type TargetConfiguration struct {
	Browser          *BrowserBinding `json:"browser,omitempty"`
	TargetID         string          `json:"targetId"`
	ExpectedRevision int             `json:"expectedRevision"`
	ProviderID       string          `json:"providerId"`
	ProviderVersion  int             `json:"providerVersion"`
	SpaceID          string          `json:"spaceId,omitempty"`
	Label            string          `json:"label"`
	Capabilities     []string        `json:"capabilities"`
	// App IDs are an explicit cross-app access ceiling. This does not grant the
	// apps scopes or approve effects. Trusted Misty controls remain the owner.
	CallerApps []string `json:"callerApps"`
}

func ValidID(id string) bool { return validUUID(id) }

// Native device records use device_<uuid>; accept legacy UUID identities too.
func ValidDeviceID(id string) bool { return validUUID(strings.TrimPrefix(id, "device_")) }
func (r TargetResolve) Validate() error {
	if r.SpaceID != "" && !opaqueResourceID.MatchString(r.SpaceID) || r.ProviderID != "" && !ValidProviderID(r.ProviderID) || !ValidName(r.Capability) || r.TargetID != "" && !validUUID(r.TargetID) || r.ContextID != "" && !validUUID(r.ContextID) {
		return ErrInvalid
	}
	return nil
}
func (r TargetConfiguration) Validate() error {
	if !validUUID(r.TargetID) || r.ExpectedRevision < 0 || r.ExpectedRevision >= 2147483647 || !ValidProviderID(r.ProviderID) || !validVersion(r.ProviderVersion) || r.SpaceID != "" && !opaqueResourceID.MatchString(r.SpaceID) || !textWithin(r.Label, 1, 200) || len(r.Capabilities) < 1 || len(r.Capabilities) > 100 || !unique(r.Capabilities) || r.CallerApps == nil || len(r.CallerApps) > 100 || !unique(r.CallerApps) {
		return ErrInvalid
	}
	for _, name := range r.Capabilities {
		if !ValidName(name) {
			return ErrInvalid
		}
	}
	for _, app := range r.CallerApps {
		if !appPattern.MatchString(app) {
			return ErrInvalid
		}
	}
	return nil
}
func (t Target) ValidateBackend(provider Provider) (BackendBinding, error) {
	var binding BackendBinding
	if !ValidProviderID(provider.ID) || !validVersion(provider.Version) || !validUUID(provider.Route.ConnectionID) || !validUUID(t.ID) || !validVersion(t.Revision) || t.ProviderID != provider.ID || t.ProviderVersion != provider.Version || t.AppID != strings.Split(provider.ID, "/")[0] || !textWithin(t.Label, 1, 200) || t.SpaceID != "" && !opaqueResourceID.MatchString(t.SpaceID) {
		return binding, ErrInvalid
	}
	if Decode(t.Binding, &binding) != nil || binding.Kind != "backend" || provider.Route.Kind != "backend" || binding.ConnectionID != provider.Route.ConnectionID {
		return binding, ErrInvalid
	}
	return binding, nil
}
func HasScopes(granted, required []string) bool {
	for _, scope := range required {
		if !slices.Contains(granted, scope) {
			return false
		}
	}
	return true
}

// BrowserBinding is authorized in trusted controls. It identifies an intended
// account/profile, not proof that the website currently has that account active.
type BrowserBinding struct {
	Kind             string   `json:"kind"`
	DeviceID         string   `json:"deviceId"`
	ProfileID        string   `json:"profileId"`
	AccountBindingID string   `json:"accountBindingId"`
	Origins          []string `json:"origins"`
	ContextID        string   `json:"contextId,omitempty"`
	ScopeID          string   `json:"scopeId,omitempty"`
	AccountIdentity  string   `json:"accountIdentity,omitempty"`
}

func (b BrowserBinding) Validate(provider Provider) error {
	if len(b.ScopeID) > 256 || len(b.AccountIdentity) > 320 {
		return ErrInvalid
	}
	if b.Kind != "browser" || provider.Route.Kind != "browser" || !ValidDeviceID(b.DeviceID) || !validUUID(b.AccountBindingID) || b.ContextID != "" && !validUUID(b.ContextID) || !profileDigest.MatchString(b.ProfileID) || len(b.Origins) < 1 || len(b.Origins) > 32 || !unique(b.Origins) {
		return ErrInvalid
	}
	for _, origin := range b.Origins {
		if !ValidOrigin(origin) || !slices.Contains(provider.Route.Origins, origin) {
			return ErrInvalid
		}
	}
	return nil
}
func (t Target) ValidateBrowser(provider Provider) (BrowserBinding, error) {
	var binding BrowserBinding
	if !ValidProviderID(provider.ID) || !validVersion(provider.Version) || !validUUID(t.ID) || !validVersion(t.Revision) || t.ProviderID != provider.ID || t.ProviderVersion != provider.Version || t.AppID != strings.Split(provider.ID, "/")[0] || !textWithin(t.Label, 1, 200) || t.SpaceID != "" && !opaqueResourceID.MatchString(t.SpaceID) {
		return binding, ErrInvalid
	}
	if Decode(t.Binding, &binding) != nil {
		return binding, ErrInvalid
	}
	return binding, binding.Validate(provider)
}

// TargetRecord is for trusted user controls, not model discovery. Disabled or
// unavailable targets remain visible so the user can repair or revoke them.
type TargetRecord struct {
	Target       Target   `json:"target"`
	Capabilities []string `json:"capabilities"`
	CallerApps   []string `json:"callerApps"`
	Enabled      bool     `json:"enabled"`
}
type TargetPage struct {
	Targets    []TargetRecord `json:"targets"`
	NextCursor *string        `json:"nextCursor"`
}
