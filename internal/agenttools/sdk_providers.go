package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/browseractions"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

// ProviderBinding preserves semantic and implementation identities independently.
// The opaque model tool name is only a registry key, never an authorization grant.
type ProviderBinding struct {
	AdapterVersion    string
	Capability        string
	CapabilityVersion int
	ProviderID        string
	ProviderVersion   int
	TargetID          string
	TargetRevision    int
	RequiredScopes    []string
}

func ProviderToolName(binding ProviderBinding) string {
	identity, _ := json.Marshal([]any{binding.ProviderID, binding.ProviderVersion, binding.Capability, binding.CapabilityVersion, binding.TargetID, binding.TargetRevision})
	hash := sha256.Sum256(identity)
	return "sdk." + hex.EncodeToString(hash[:])
}

// ProviderRegistration extends the existing registry. Its handler must resolve
// current target authority again immediately before dispatch and journal effects.
// This constructor cannot install a provider or approve an invocation.
func ProviderRegistration(provider cap.Provider, target cap.Target, definition cap.Definition, handler Handler) (Registration, error) {
	if handler == nil {
		return Registration{}, ErrInvalidRegistration
	}
	var targetErr error
	switch provider.Route.Kind {
	case "server":
		targetErr = target.ValidatePlanner(provider)
	case "backend":
		_, targetErr = target.ValidateBackend(provider)
	case "browser":
		_, targetErr = target.ValidateBrowser(provider)
	default:
		targetErr = cap.ErrInvalid
	}
	if targetErr != nil {
		return Registration{}, ErrInvalidRegistration
	}
	expected, err := json.Marshal(definition)
	if err != nil {
		return Registration{}, ErrInvalidRegistration
	}
	found := false
	for _, candidate := range provider.Capabilities {
		raw, _ := json.Marshal(candidate)
		if cap.EqualJSON(raw, expected) {
			found = true
			break
		}
	}
	if !found || definition.Validate() != nil {
		return Registration{}, ErrInvalidRegistration
	}
	input, err := cap.CompileSchema(definition.InputSchema)
	if err != nil {
		return Registration{}, err
	}
	output, err := cap.CompileSchema(definition.OutputSchema)
	if err != nil {
		return Registration{}, err
	}
	binding := ProviderBinding{AdapterVersion: cap.ExecutionAdapterVersion(provider), Capability: definition.Name, CapabilityVersion: definition.Version, ProviderID: provider.ID, ProviderVersion: provider.Version, TargetID: target.ID, TargetRevision: target.Revision, RequiredScopes: append([]string{}, definition.RequiredScopes...)}
	risk, approval := serveragent.RiskRead, ApprovalNone
	// Arbitrary adapters do not inherit a weaker approval because their manifest
	// says "scoped". Standing permission is resolved by trusted run controls.
	if definition.Effects.Kind != "read" || len(definition.Effects.Incidental) > 0 || definition.Effects.Approval != "none" {
		risk, approval = serveragent.RiskWrite, ApprovalInteractive
	}
	// Only the shipped, bounded mail preparation adapters admit autonomous preparation.
	if _, err := browseractions.Pilots.Resolve(provider, definition.Name); err == nil && (definition.Name == "inbox.read" || definition.Name == "inbox.draft") {
		approval = ApprovalNone
	}
	if definition.Effects.Kind == "execute" || definition.Effects.Kind == "destructive" {
		risk = serveragent.RiskDangerous
	}
	description := fmt.Sprintf("%s\nCapability: %s v%d. Provider: %s v%d. Target: %s (%s; revision %d).", definition.Description, definition.Name, definition.Version, provider.ID, provider.Version, target.Label, target.ID, target.Revision)
	if provider.ID == cap.PlannerProviderID {
		description += " For an unqualified task request, visibly use this originating Space Planner. Resolved destination: " + cap.PlannerContainer(target.SpaceID) + "; label: " + target.Label + ". Explicit external destinations take precedence; never substitute Planner for an unavailable requested provider."
	}
	return Registration{Descriptor: Descriptor{
		Name: ProviderToolName(binding), Version: definition.Version,
		Description: description,
		Risk:        risk, InputSchema: definition.InputSchema, OutputSchema: definition.OutputSchema,
		RequiredPermission: definition.RequiredScopes[0], Approval: approval, Locality: LocalityProvider,
		Idempotent: definition.Effects.Retry == "idempotent" || definition.Effects.Retry == "read_only",
		AuditEvent: "sdk.capability", ProviderBinding: &binding,
	}, Handler: func(ctx context.Context, invocation Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
		var value any
		if len(request.Arguments) > 512<<10 || json.Unmarshal(request.Arguments, &value) != nil || input.Validate(value) != nil {
			return nil, ErrArgumentsInvalid
		}
		result, err := handler(ctx, invocation, request)
		if err != nil {
			return nil, err
		}
		if len(result) > 512<<10 || json.Unmarshal(result, &value) != nil || output.Validate(value) != nil {
			return nil, ErrResultInvalid
		}
		return result, nil
	}}, nil
}
