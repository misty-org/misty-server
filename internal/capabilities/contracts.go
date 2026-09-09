// Package capabilities contains provider wire contracts. Executable tools are
// still resolved and dispatched by agenttools; a declaration grants no authority.
package capabilities

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
)

var ErrInvalid = errors.New("invalid capability declaration")
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
var scopePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$`)
var appPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{1,79}$`)
var providerPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]+/[a-z][a-z0-9_-]*$`)

const MaxManifestBytes = 2 << 20
const SignatureDomain = "misty.sdk.manifest.v1\n"

type Effects struct {
	Kind       string   `json:"kind"`
	Incidental []string `json:"incidental"`
	Approval   string   `json:"approval"`
	Retry      string   `json:"retry"`
}

// Match the SDK's optional incidental-effects default without serializing null.
func (effects *Effects) UnmarshalJSON(raw []byte) error {
	type wire Effects
	var value wire
	if err := Decode(raw, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ErrInvalid
	}
	if incidental, exists := fields["incidental"]; exists && bytes.Equal(bytes.TrimSpace(incidental), []byte("null")) {
		return ErrInvalid
	}
	if value.Incidental == nil {
		value.Incidental = []string{}
	}
	*effects = Effects(value)
	return nil
}

type Definition struct {
	Name           string          `json:"name"`
	Version        int             `json:"version"`
	Description    string          `json:"description"`
	InputSchema    json.RawMessage `json:"inputSchema"`
	OutputSchema   json.RawMessage `json:"outputSchema"`
	RequiredScopes []string        `json:"requiredScopes"`
	Effects        Effects         `json:"effects"`
}
type Route struct {
	Kind           string   `json:"kind"`
	Adapter        string   `json:"adapter,omitempty"`
	AdapterVersion int      `json:"adapterVersion,omitempty"`
	Origins        []string `json:"origins,omitempty"`
	Hints          []string `json:"hints,omitempty"`
	ConnectionID   string   `json:"connectionId,omitempty"`
	InstanceID     string   `json:"instanceId,omitempty"`
}

func (route *Route) UnmarshalJSON(raw []byte) error {
	type wire Route
	var value wire
	if err := Decode(raw, &value); err != nil {
		return err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ErrInvalid
	}
	allowed := map[string][]string{
		"browser": {"kind", "origins", "hints", "adapter", "adapterVersion"}, "backend": {"kind", "connectionId"},
		"view": {"kind", "instanceId"}, "native": {"kind", "adapter"}, "server": {"kind", "adapter"},
	}
	names, ok := allowed[value.Kind]
	if !ok {
		return ErrInvalid
	}
	for name := range fields {
		if !slices.Contains(names, name) {
			return ErrInvalid
		}
	}
	if hints, exists := fields["hints"]; exists && bytes.Equal(bytes.TrimSpace(hints), []byte("null")) {
		return ErrInvalid
	}
	if value.Kind == "browser" {
		_, hasAdapter := fields["adapter"]
		_, hasVersion := fields["adapterVersion"]
		if hasAdapter != hasVersion || hasAdapter && (value.Adapter == "" || !validVersion(value.AdapterVersion)) {
			return ErrInvalid
		}
	}
	if value.Kind == "browser" && value.Hints == nil {
		value.Hints = []string{}
	}
	*route = Route(value)
	return nil
}

type Provider struct {
	ID           string       `json:"id"`
	Version      int          `json:"version"`
	Label        string       `json:"label"`
	Route        Route        `json:"route"`
	Capabilities []Definition `json:"capabilities"`
}
type Manifest struct {
	Protocol  int        `json:"protocol"`
	Providers []Provider `json:"providers"`
}

// InstallDocument is reviewed in trusted Misty controls. The first install pins
// its publisher key; a signature proves continuity, not a publisher's reputation.
type InstallDocument struct {
	AppID             string   `json:"appId"`
	Version           string   `json:"version"`
	PermissionVersion int      `json:"permissionVersion"`
	Scopes            []string `json:"scopes"`
	Capabilities      Manifest `json:"capabilities"`
}
type SignedManifest struct {
	Document  string `json:"document"`
	PublicKey string `json:"publicKey"`
	Signature string `json:"signature"`
}
type VerifiedManifest struct {
	InstallDocument
	Digest    string
	PublicKey []byte
	Document  string
	Signature []byte
}
type Availability struct {
	State      string    `json:"state"`
	ObservedAt time.Time `json:"observedAt"`
	Reason     string    `json:"reason,omitempty"`
}

func Decode(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > MaxManifestBytes {
		return ErrInvalid
	}
	// Reject duplicate object keys before decoding: signatures, schemas and host
	// parsers must agree about which value is being authorized.
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 40 {
			return ErrInvalid
		}
		tok, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return ErrInvalid
				}
				seen[key] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		default:
			return ErrInvalid
		}
		_, err = d.Token()
		return err
	}
	if err := walk(0); err != nil {
		return ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

func Verify(signed SignedManifest) (*VerifiedManifest, error) {
	key, err := base64.StdEncoding.DecodeString(signed.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, ErrInvalid
	}
	signature, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || len(signed.Document) > MaxManifestBytes {
		return nil, ErrInvalid
	}
	if !ed25519.Verify(key, []byte(SignatureDomain+signed.Document), signature) {
		return nil, ErrInvalid
	}
	var document InstallDocument
	if err := Decode([]byte(signed.Document), &document); err != nil {
		return nil, err
	}
	if !appPattern.MatchString(document.AppID) || !textWithin(document.Version, 1, 40) || !validVersion(document.PermissionVersion) || document.Scopes == nil || len(document.Scopes) > 128 || !unique(document.Scopes) || document.Capabilities.Protocol != 1 || document.Capabilities.Providers == nil || len(document.Capabilities.Providers) > 32 {
		return nil, ErrInvalid
	}
	for _, scope := range document.Scopes {
		if !validScope(scope) {
			return nil, ErrInvalid
		}
	}
	providers := map[string]bool{}
	for _, provider := range document.Capabilities.Providers {
		if providers[provider.ID] {
			return nil, ErrInvalid
		}
		providers[provider.ID] = true
		if err := provider.Validate(document.AppID, document.Scopes); err != nil {
			return nil, err
		}
	}
	digest := sha256.Sum256([]byte(signed.Document))
	return &VerifiedManifest{InstallDocument: document, Digest: hex.EncodeToString(digest[:]), PublicKey: key, Document: signed.Document, Signature: signature}, nil
}
func ValidProviderID(id string) bool         { return len(id) <= 240 && providerPattern.MatchString(id) }
func ValidName(name string) bool             { return len(name) <= 160 && namePattern.MatchString(name) }
func validVersion(v int) bool                { return v > 0 && v <= 2147483647 }
func validScope(v string) bool               { return len(v) <= 160 && scopePattern.MatchString(v) }
func textWithin(s string, min, max int) bool { return len([]rune(s)) >= min && len([]rune(s)) <= max }
func unique(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}
func validUUID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id.String() == strings.ToLower(s)
}

func asciiHost(host string) bool {
	for _, c := range host {
		if c > 127 {
			return false
		}
	}
	return true
}

// ValidOrigin accepts only canonical, exact HTTPS origins, never URL prefixes.
func ValidOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && len(origin) <= 2048 && u.Scheme == "https" && u.Hostname() != "" && u.Host == strings.ToLower(u.Host) && u.Port() != "443" && asciiHost(u.Host) && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && !u.ForceQuery && u.String() == origin
}

func (p Provider) Validate(appID string, scopes []string) error {
	if !ValidProviderID(p.ID) || strings.Split(p.ID, "/")[0] != appID || !validVersion(p.Version) || !textWithin(p.Label, 1, 200) || len(p.Capabilities) < 1 || len(p.Capabilities) > 100 {
		return ErrInvalid
	}
	// Downloaded app registrations never create host adapters. Built-in adapters
	// are installed by host code, not through this public installation boundary.
	if p.Route.Kind != "browser" && p.Route.AdapterVersion != 0 {
		return ErrInvalid
	}
	switch p.Route.Kind {
	case "browser":
		if (p.Route.Adapter == "") != (p.Route.AdapterVersion == 0) || p.Route.Adapter != "" && (!regexp.MustCompile(`^[a-z][a-z0-9_-]{0,79}$`).MatchString(p.Route.Adapter) || !validVersion(p.Route.AdapterVersion)) {
			return ErrInvalid
		}
		if len(p.Route.Origins) < 1 || len(p.Route.Origins) > 32 || !unique(p.Route.Origins) || len(p.Route.Hints) > 20 || p.Route.ConnectionID != "" || p.Route.InstanceID != "" || !slices.Contains(scopes, "browser.inspect") {
			return ErrInvalid
		}
		for _, origin := range p.Route.Origins {
			if !ValidOrigin(origin) {
				return ErrInvalid
			}
		}
		for _, hint := range p.Route.Hints {
			if !textWithin(hint, 0, 1000) {
				return ErrInvalid
			}
		}
	case "backend":
		if !validUUID(p.Route.ConnectionID) || p.Route.Adapter != "" || p.Route.InstanceID != "" || len(p.Route.Origins) > 0 || len(p.Route.Hints) > 0 {
			return ErrInvalid
		}
	case "view":
		if !validUUID(p.Route.InstanceID) || p.Route.Adapter != "" || p.Route.ConnectionID != "" || len(p.Route.Origins) > 0 || len(p.Route.Hints) > 0 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, definition := range p.Capabilities {
		if seen[definition.Name] {
			return ErrInvalid
		}
		seen[definition.Name] = true
		if err := definition.Validate(); err != nil {
			return err
		}
		for _, required := range definition.RequiredScopes {
			if !slices.Contains(scopes, required) {
				return ErrInvalid
			}
		}
		if p.Route.Kind == "browser" && definition.Effects.Kind != "read" && !slices.Contains(scopes, "browser.interact") {
			return ErrInvalid
		}
	}
	return nil
}
func (d Definition) Validate() error {
	if err := validateBuiltinContract(d); err != nil {
		return err
	}
	if !ValidName(d.Name) || !validVersion(d.Version) || !textWithin(d.Description, 1, 2000) || len(d.RequiredScopes) < 1 || len(d.RequiredScopes) > 32 || !unique(d.RequiredScopes) {
		return ErrInvalid
	}
	for _, scope := range d.RequiredScopes {
		if !validScope(scope) {
			return ErrInvalid
		}
	}
	e := d.Effects
	if !slices.Contains([]string{"read", "write", "send", "execute", "destructive"}, e.Kind) || !slices.Contains([]string{"none", "scoped", "interactive"}, e.Approval) || !slices.Contains([]string{"read_only", "idempotent", "reconcile", "never"}, e.Retry) || len(e.Incidental) > 16 {
		return ErrInvalid
	}
	for _, effect := range e.Incidental {
		if !textWithin(effect, 1, 300) {
			return ErrInvalid
		}
	}
	if e.Kind != "read" && (e.Approval == "none" || e.Retry == "read_only") {
		return ErrInvalid
	}
	if _, err := CompileSchema(d.InputSchema); err != nil {
		return err
	}
	if _, err := CompileSchema(d.OutputSchema); err != nil {
		return err
	}
	return nil
}
func CompileSchema(raw json.RawMessage) (*jsonschema.Resolved, error) {
	if len(raw) > 64<<10 {
		return nil, ErrInvalid
	}
	var value map[string]any
	if err := Decode(raw, &value); err != nil || value == nil {
		return nil, ErrInvalid
	}
	var safe func(any, int) bool
	safe = func(v any, depth int) bool {
		if depth > 32 {
			return false
		}
		switch v := v.(type) {
		case map[string]any:
			for key, child := range v {
				if slices.Contains([]string{"__proto__", "constructor", "prototype", "$dynamicRef"}, key) {
					return false
				}
				if key == "$ref" {
					ref, ok := child.(string)
					if !ok || !strings.HasPrefix(ref, "#/$defs/") {
						return false
					}
				}
				if !safe(child, depth+1) {
					return false
				}
			}
		case []any:
			for _, child := range v {
				if !safe(child, depth+1) {
					return false
				}
			}
		}
		return true
	}
	if !safe(value, 0) {
		return nil, ErrInvalid
	}
	var schema jsonschema.Schema
	if json.Unmarshal(raw, &schema) != nil {
		return nil, ErrInvalid
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, fmt.Errorf("%w: schema cannot be resolved", ErrInvalid)
	}
	return resolved, nil
}
func (a Availability) Validate(now time.Time) error {
	if !slices.Contains([]string{"available", "device_required", "authentication_required", "account_confirmation_required", "view_closed", "unavailable", "revoked"}, a.State) || a.ObservedAt.IsZero() || a.ObservedAt.After(now.Add(time.Minute)) || a.ObservedAt.Before(now.Add(-5*time.Minute)) || !textWithin(a.Reason, 0, 1000) {
		return ErrInvalid
	}
	return nil
}

// EqualJSON compares parsed values, independent of whitespace or object order.
func EqualJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	aa, _ := json.Marshal(x)
	bb, _ := json.Marshal(y)
	return bytes.Equal(aa, bb)
}

// ExecutionAdapterVersion is persisted at admission, independently of provider
// and capability versions. Unknown browser implementations remain unavailable.
func ExecutionAdapterVersion(p Provider) string {
	if p.ID == PlannerProviderID && p.Route.Kind == "server" && p.Route.Adapter == "planner" {
		return "sdk-planner-v1"
	}
	if p.Route.Kind == "backend" {
		return "sdk-backend-v1"
	}
	if p.Route.Kind == "browser" && p.Route.Adapter != "" && p.Route.AdapterVersion > 0 {
		return fmt.Sprintf("sdk-browser:%s:v%d", p.Route.Adapter, p.Route.AdapterVersion)
	}
	return ""
}

// CanonicalJSON gives typed values and decoded durable records the same digest.
func CanonicalJSON(raw []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
