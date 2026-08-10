package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

type ApprovalPolicy string

const (
	ApprovalNone           ApprovalPolicy = "none"
	ApprovalExplicitIntent ApprovalPolicy = "explicit_intent"
	ApprovalInteractive    ApprovalPolicy = "interactive"
)

type Locality string

const (
	LocalityServer   Locality = "server"
	LocalityDevice   Locality = "device"
	LocalityProvider Locality = "provider"
)

var (
	ErrInvalidRegistration = errors.New("invalid Agent Toolbox registration")
	ErrToolNotFound        = errors.New("Agent Toolbox tool not found")
	ErrCapabilityDenied    = errors.New("Agent Toolbox capability denied")
	ErrApprovalRequired    = errors.New("Agent Toolbox approval required")
	ErrArgumentsInvalid    = errors.New("Agent Toolbox arguments do not match the declared schema")
	ErrResultInvalid       = errors.New("Agent Toolbox result does not match the declared schema")
)

type Descriptor struct {
	Name               string
	Version            int
	Description        string
	Risk               string
	InputSchema        json.RawMessage
	OutputSchema       json.RawMessage
	RequiredPermission string
	AgentPermission    string
	AllowCustomAgent   bool
	OwnerOnly          bool
	Approval           ApprovalPolicy
	ApprovalBySource   map[string]ApprovalPolicy
	Locality           Locality
	Idempotent         bool
	AuditEvent         string
	Aliases            []string
	Sources            []string
	Triggers           []string
}

type Invocation struct {
	UserID                string
	SpaceID               string
	AgentID               string
	AgentInstanceID       string
	RunID                 string
	SessionID             string
	Source                string
	Trigger               string
	OriginalInput         string
	ExplicitTools         map[string]bool
	ApprovedTools         map[string]bool
	ConversationScopeKind string
	ConversationID        string
	// DelegatedApproval is used by runtimes that persist and resume approval
	// inside the tool handler. The descriptor still advertises the real policy;
	// the handler remains responsible for enforcing it before any external write.
	DelegatedApproval bool
}

type Handler func(context.Context, Invocation, serveragent.ToolRequest) (json.RawMessage, error)

type Authorizer func(context.Context, Invocation, Descriptor) (bool, error)

type ExecutionMiddleware func(context.Context, Invocation, Descriptor, serveragent.ToolRequest, Handler) (json.RawMessage, error)

type Registration struct {
	Descriptor Descriptor
	Handler    Handler
}

type registeredTool struct {
	descriptor Descriptor
	handler    Handler
}

type Registry struct {
	tools   map[string]registeredTool
	aliases map[string]string
	ordered []string
}

func New(registrations ...Registration) (*Registry, error) {
	registry := &Registry{tools: map[string]registeredTool{}, aliases: map[string]string{}}
	for _, registration := range registrations {
		descriptor := normalizeDescriptor(registration.Descriptor)
		if err := validateRegistration(descriptor, registration.Handler); err != nil {
			return nil, err
		}
		if _, exists := registry.tools[descriptor.Name]; exists {
			return nil, fmt.Errorf("%w: duplicate tool %s", ErrInvalidRegistration, descriptor.Name)
		}
		if _, exists := registry.aliases[descriptor.Name]; exists {
			return nil, fmt.Errorf("%w: tool conflicts with alias %s", ErrInvalidRegistration, descriptor.Name)
		}
		registry.tools[descriptor.Name] = registeredTool{descriptor: descriptor, handler: registration.Handler}
		registry.ordered = append(registry.ordered, descriptor.Name)
		for _, alias := range descriptor.Aliases {
			if _, exists := registry.tools[alias]; exists {
				return nil, fmt.Errorf("%w: alias conflicts with tool %s", ErrInvalidRegistration, alias)
			}
			if _, exists := registry.aliases[alias]; exists {
				return nil, fmt.Errorf("%w: duplicate alias %s", ErrInvalidRegistration, alias)
			}
			registry.aliases[alias] = descriptor.Name
		}
	}
	return registry, nil
}

func MustNew(registrations ...Registration) *Registry {
	registry, err := New(registrations...)
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) Descriptors() []Descriptor {
	if r == nil {
		return nil
	}
	items := make([]Descriptor, 0, len(r.ordered))
	for _, name := range r.ordered {
		items = append(items, cloneDescriptor(r.tools[name].descriptor))
	}
	return items
}

func (r *Registry) Resolve(ctx context.Context, invocation Invocation, requested []string, authorize Authorizer) (serveragent.ToolManifest, error) {
	manifest := serveragent.ToolManifest{Tools: []serveragent.ToolDefinition{}}
	seen := map[string]bool{}
	for _, requestedName := range requested {
		tool, _, ok := r.lookup(requestedName)
		if !ok || seen[tool.descriptor.Name] {
			continue
		}
		allowed, err := toolAllowed(ctx, invocation, tool.descriptor, authorize)
		if err != nil {
			return serveragent.ToolManifest{}, err
		}
		if !allowed {
			continue
		}
		seen[tool.descriptor.Name] = true
		manifest.Tools = append(manifest.Tools, serveragent.ToolDefinition{Name: tool.descriptor.Name, Description: tool.descriptor.Description, Risk: tool.descriptor.Risk, InputSchema: append(json.RawMessage(nil), tool.descriptor.InputSchema...)})
	}
	return manifest, nil
}

func (r *Registry) Execute(ctx context.Context, invocation Invocation, request serveragent.ToolRequest, authorize Authorizer) (json.RawMessage, error) {
	return r.ExecuteWithMiddleware(ctx, invocation, request, authorize, nil)
}

func (r *Registry) ExecuteWithMiddleware(ctx context.Context, invocation Invocation, request serveragent.ToolRequest, authorize Authorizer, middleware ExecutionMiddleware) (json.RawMessage, error) {
	tool, canonicalName, ok := r.lookup(request.Name)
	if !ok {
		return nil, ErrToolNotFound
	}
	allowed, err := toolAllowed(ctx, invocation, tool.descriptor, authorize)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrCapabilityDenied
	}
	if approvalPolicy(invocation, tool.descriptor) == ApprovalInteractive && !invocation.DelegatedApproval && !toolFlag(invocation.ApprovedTools, tool.descriptor) {
		return nil, ErrApprovalRequired
	}
	if len(request.Arguments) == 0 {
		request.Arguments = json.RawMessage(`{}`)
	}
	if len(request.Arguments) > 256<<10 || !matchesSchema(tool.descriptor.InputSchema, request.Arguments) {
		return nil, ErrArgumentsInvalid
	}
	request.Name = canonicalName
	execute := func(nextCtx context.Context, nextInvocation Invocation, nextRequest serveragent.ToolRequest) (json.RawMessage, error) {
		result, executeErr := tool.handler(nextCtx, nextInvocation, nextRequest)
		if executeErr != nil {
			return nil, executeErr
		}
		if len(result) > serveragent.MaxToolResultBytes || !matchesSchema(tool.descriptor.OutputSchema, result) {
			return nil, ErrResultInvalid
		}
		return result, nil
	}
	var result json.RawMessage
	if middleware != nil {
		result, err = middleware(ctx, invocation, cloneDescriptor(tool.descriptor), request, execute)
	} else {
		result, err = execute(ctx, invocation, request)
	}
	if err != nil {
		return nil, err
	}
	if len(result) > serveragent.MaxToolResultBytes || !matchesSchema(tool.descriptor.OutputSchema, result) {
		return nil, ErrResultInvalid
	}
	return result, nil
}

func matchesSchema(rawSchema, value json.RawMessage) bool {
	if !json.Valid(value) {
		return false
	}
	var schema workflowv2.JSONSchema
	if len(rawSchema) > 0 && json.Unmarshal(rawSchema, &schema) != nil {
		return false
	}
	return workflowv2.ValidateJSON(schema, value) == nil
}

func (r *Registry) lookup(name string) (registeredTool, string, bool) {
	if r == nil {
		return registeredTool{}, "", false
	}
	name = strings.TrimSpace(name)
	canonical := name
	if resolved, ok := r.aliases[name]; ok {
		canonical = resolved
	}
	tool, ok := r.tools[canonical]
	return tool, canonical, ok
}

func toolAllowed(ctx context.Context, invocation Invocation, descriptor Descriptor, authorize Authorizer) (bool, error) {
	if !stringAllowed(invocation.Source, descriptor.Sources) || !stringAllowed(invocation.Trigger, descriptor.Triggers) {
		return false, nil
	}
	if approvalPolicy(invocation, descriptor) == ApprovalExplicitIntent && !toolFlag(invocation.ExplicitTools, descriptor) {
		return false, nil
	}
	if authorize == nil {
		return true, nil
	}
	return authorize(ctx, invocation, descriptor)
}

func stringAllowed(value string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func toolFlag(values map[string]bool, descriptor Descriptor) bool {
	if values[descriptor.Name] {
		return true
	}
	for _, alias := range descriptor.Aliases {
		if values[alias] {
			return true
		}
	}
	return false
}

func approvalPolicy(invocation Invocation, descriptor Descriptor) ApprovalPolicy {
	if policy, ok := descriptor.ApprovalBySource[invocation.Source]; ok {
		return policy
	}
	return descriptor.Approval
}

func normalizeDescriptor(descriptor Descriptor) Descriptor {
	descriptor.Name = strings.TrimSpace(descriptor.Name)
	descriptor.Description = strings.TrimSpace(descriptor.Description)
	descriptor.RequiredPermission = strings.TrimSpace(descriptor.RequiredPermission)
	descriptor.AgentPermission = strings.TrimSpace(descriptor.AgentPermission)
	descriptor.AuditEvent = strings.TrimSpace(descriptor.AuditEvent)
	if descriptor.Version == 0 {
		descriptor.Version = 1
	}
	if descriptor.Approval == "" {
		descriptor.Approval = ApprovalNone
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
	if len(descriptor.ApprovalBySource) > 0 {
		overrides := make(map[string]ApprovalPolicy, len(descriptor.ApprovalBySource))
		for source, policy := range descriptor.ApprovalBySource {
			source = strings.TrimSpace(source)
			if source != "" {
				overrides[source] = policy
			}
		}
		descriptor.ApprovalBySource = overrides
	}
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
	if descriptor.Risk != serveragent.RiskRead && descriptor.Risk != serveragent.RiskWrite && descriptor.Risk != serveragent.RiskDangerous {
		return fmt.Errorf("%w: invalid risk for %s", ErrInvalidRegistration, descriptor.Name)
	}
	if descriptor.Approval != ApprovalNone && descriptor.Approval != ApprovalExplicitIntent && descriptor.Approval != ApprovalInteractive {
		return fmt.Errorf("%w: invalid approval policy for %s", ErrInvalidRegistration, descriptor.Name)
	}
	for source, policy := range descriptor.ApprovalBySource {
		if policy != ApprovalNone && policy != ApprovalExplicitIntent && policy != ApprovalInteractive {
			return fmt.Errorf("%w: invalid approval policy for %s source %s", ErrInvalidRegistration, descriptor.Name, source)
		}
	}
	if descriptor.Locality != LocalityServer && descriptor.Locality != LocalityDevice && descriptor.Locality != LocalityProvider {
		return fmt.Errorf("%w: invalid locality for %s", ErrInvalidRegistration, descriptor.Name)
	}
	if descriptor.Risk != serveragent.RiskRead && descriptor.AuditEvent == "" {
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
	if len(descriptor.ApprovalBySource) > 0 {
		overrides := make(map[string]ApprovalPolicy, len(descriptor.ApprovalBySource))
		for source, policy := range descriptor.ApprovalBySource {
			overrides[source] = policy
		}
		descriptor.ApprovalBySource = overrides
	}
	return descriptor
}
