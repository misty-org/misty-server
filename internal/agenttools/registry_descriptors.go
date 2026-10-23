package agenttools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/kannachi323/misty/server/internal/agent"
)

// Compile once at registration so every tool shares the same JSON Schema
// semantics.
func compileToolSchema(raw json.RawMessage) (*jsonschema.Resolved, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	return compileSchema(raw)
}

func matchesSchema(schema *jsonschema.Resolved, raw json.RawMessage) bool {
	var value any
	return schema != nil && json.Unmarshal(raw, &value) == nil && schema.Validate(value) == nil
}

func normalizeDescriptor(descriptor Descriptor) Descriptor {
	// Agents share their owner’s tool access and never require a second
	// per-action grant; executors still authorize targets.
	descriptor.Name = strings.TrimSpace(descriptor.Name)
	descriptor.Description = strings.TrimSpace(descriptor.Description)
	descriptor.RequiredPermission = strings.TrimSpace(descriptor.RequiredPermission)
	descriptor.AuditEvent = strings.TrimSpace(descriptor.AuditEvent)
	if descriptor.Version == 0 {
		descriptor.Version = 1
	}
	if descriptor.Locality == "" {
		descriptor.Locality = LocalityServer
	}
	aliases := make([]string, 0, len(descriptor.Aliases))
	for _, alias := range descriptor.Aliases {
		alias = strings.TrimSpace(alias)
		if alias != "" && alias != descriptor.Name {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	descriptor.Aliases = aliases
	descriptor.Sources = normalizeStrings(descriptor.Sources)
	descriptor.Triggers = normalizeStrings(descriptor.Triggers)
	return descriptor
}

func normalizeStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func validateRegistration(descriptor Descriptor, handler Handler) error {
	if descriptor.Name == "" || descriptor.Description == "" || handler == nil || descriptor.Version < 1 {
		return ErrInvalidRegistration
	}
	if descriptor.Risk != agent.RiskRead && descriptor.Risk != agent.RiskWrite && descriptor.Risk != agent.RiskDangerous {
		return fmt.Errorf("%w: invalid risk for %s", ErrInvalidRegistration, descriptor.Name)
	}
	if descriptor.Locality != LocalityServer && descriptor.Locality != LocalityDevice && descriptor.Locality != LocalityProvider && descriptor.Locality != LocalityRouted {
		return fmt.Errorf("%w: invalid locality for %s", ErrInvalidRegistration, descriptor.Name)
	}
	if descriptor.Layer != LayerPublic && descriptor.Layer != LayerPrivate {
		return fmt.Errorf("%w: tool %s declares no layer", ErrInvalidRegistration, descriptor.Name)
	}
	if descriptor.Risk != agent.RiskRead && descriptor.AuditEvent == "" {
		return fmt.Errorf("%w: write tool %s has no audit event", ErrInvalidRegistration, descriptor.Name)
	}
	if len(descriptor.InputSchema) > 0 && !json.Valid(descriptor.InputSchema) {
		return fmt.Errorf("%w: invalid input schema for %s", ErrInvalidRegistration, descriptor.Name)
	}
	if len(descriptor.OutputSchema) > 0 && !json.Valid(descriptor.OutputSchema) {
		return fmt.Errorf("%w: invalid output schema for %s", ErrInvalidRegistration, descriptor.Name)
	}
	return nil
}

func cloneDescriptor(descriptor Descriptor) Descriptor {
	descriptor.InputSchema = append(json.RawMessage(nil), descriptor.InputSchema...)
	descriptor.OutputSchema = append(json.RawMessage(nil), descriptor.OutputSchema...)
	descriptor.Aliases = append([]string(nil), descriptor.Aliases...)
	descriptor.Sources = append([]string(nil), descriptor.Sources...)
	descriptor.Triggers = append([]string(nil), descriptor.Triggers...)
	return descriptor
}
