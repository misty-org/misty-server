package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/kannachi323/misty/server/internal/agent"
)

type Locality string

const (
	LocalityServer   Locality = "server"
	LocalityDevice   Locality = "device"
	LocalityProvider Locality = "provider"
	// LocalityRouted tools delegate to another registry that authorizes and
	// journals the effect, such as account tools routed into one Space.
	LocalityRouted Locality = "routed"
)

// Layer is the permission layer a tool touches
// (docs/spaces/as-profiles.md#private-and-public). Public tools
// read and change the Space's shared items, which the database still filters
// to "public, or owned by the viewer". Private tools reach a person's own
// trees: memory, devices, connected apps, private chats and agent settings.
// Every registration declares one; there is no default.
type Layer string

const (
	LayerPublic  Layer = "public"
	LayerPrivate Layer = "private"
)

var (
	ErrInvalidRegistration = errors.New("invalid Agent Toolbox registration")
	ErrToolNotFound        = errors.New("Agent Toolbox tool not found")
	ErrCapabilityDenied    = errors.New("Agent Toolbox capability denied")
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
	OwnerOnly          bool
	Locality           Locality
	Layer              Layer
	Idempotent         bool
	AuditEvent         string
	Aliases            []string
	Sources            []string
	Triggers           []string
}

type Invocation struct {
	UserID  string
	SpaceID string
	// MemorySpaceID is the Space whose memory tree the run reads and writes:
	// the Space of the window the request came from. Empty falls back to
	// SpaceID, then to the asker's personal Space.
	MemorySpaceID         string
	AgentID               string
	AgentInstanceID       string
	RunID                 string
	SessionID             string
	Source                string
	Trigger               string
	OriginalInput         string
	ExplicitTools         map[string]bool
	ConversationScopeKind string
	ConversationID        string
	// PermissionMode is the run's Ask or Auto, frozen when the run was admitted.
	// Empty follows the agent's default.
	PermissionMode string
	// PublicLayerOnly marks a run the private layer doesn't open for, such as a
	// teammate's ask of someone's agent. Its tools see public items only.
	PublicLayerOnly bool
}

type Handler func(context.Context, Invocation, agent.ToolRequest) (json.RawMessage, error)

type Authorizer func(context.Context, Invocation, Descriptor) (bool, error)

type ExecutionMiddleware func(context.Context, Invocation, Descriptor, agent.ToolRequest, Handler) (json.RawMessage, error)

type Registration struct {
	Descriptor Descriptor
	Handler    Handler
}

type registeredTool struct {
	descriptor   Descriptor
	handler      Handler
	inputSchema  *jsonschema.Resolved
	outputSchema *jsonschema.Resolved
}

type Registry struct {
	tools   map[string]registeredTool
	aliases map[string]string
	ordered []string
}

func New(registrations ...Registration) (*Registry, error) {
	registry := &Registry{tools: map[string]registeredTool{}, aliases: map[string]string{}}
	for _, registration := range registrations {
		descriptor := normalizeDescriptor(cloneDescriptor(registration.Descriptor))
		if err := validateRegistration(descriptor, registration.Handler); err != nil {
			return nil, err
		}
		if _, exists := registry.tools[descriptor.Name]; exists {
			return nil, fmt.Errorf("%w: duplicate tool %s", ErrInvalidRegistration, descriptor.Name)
		}
		if _, exists := registry.aliases[descriptor.Name]; exists {
			return nil, fmt.Errorf("%w: tool conflicts with alias %s", ErrInvalidRegistration, descriptor.Name)
		}
		input, err := compileToolSchema(descriptor.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("%w: input schema for %s: %v", ErrInvalidRegistration, descriptor.Name, err)
		}
		output, err := compileToolSchema(descriptor.OutputSchema)
		if err != nil {
			return nil, fmt.Errorf("%w: output schema for %s: %v", ErrInvalidRegistration, descriptor.Name, err)
		}
		registry.tools[descriptor.Name] = registeredTool{descriptor: descriptor, handler: registration.Handler, inputSchema: input, outputSchema: output}
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

func (r *Registry) Resolve(ctx context.Context, invocation Invocation, requested []string, authorize Authorizer) (agent.ToolManifest, error) {
	manifest := agent.ToolManifest{Tools: []agent.ToolDefinition{}}
	seen := map[string]bool{}
	for _, requestedName := range requested {
		tool, _, ok := r.lookup(requestedName)
		if !ok || seen[tool.descriptor.Name] {
			continue
		}
		allowed, err := toolAllowed(ctx, invocation, tool.descriptor, authorize)
		if err != nil {
			return agent.ToolManifest{}, err
		}
		if !allowed {
			continue
		}
		seen[tool.descriptor.Name] = true
		manifest.Tools = append(manifest.Tools, agent.ToolDefinition{Name: tool.descriptor.Name, Description: tool.descriptor.Description, Risk: tool.descriptor.Risk, InputSchema: append(json.RawMessage(nil), tool.descriptor.InputSchema...)})
	}
	return manifest, nil
}

func (r *Registry) Execute(ctx context.Context, invocation Invocation, request agent.ToolRequest, authorize Authorizer) (json.RawMessage, error) {
	return r.ExecuteWithMiddleware(ctx, invocation, request, authorize, nil)
}

func (r *Registry) ExecuteWithMiddleware(ctx context.Context, invocation Invocation, request agent.ToolRequest, authorize Authorizer, middleware ExecutionMiddleware) (json.RawMessage, error) {
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
	if len(request.Arguments) == 0 {
		request.Arguments = json.RawMessage(`{}`)
	}
	if len(request.Arguments) > 256<<10 || !matchesSchema(tool.inputSchema, request.Arguments) {
		return nil, ErrArgumentsInvalid
	}
	request.Name = canonicalName
	execute := func(nextCtx context.Context, nextInvocation Invocation, nextRequest agent.ToolRequest) (json.RawMessage, error) {
		result, executeErr := tool.handler(nextCtx, nextInvocation, nextRequest)
		if executeErr != nil {
			return nil, executeErr
		}
		if len(result) > agent.MaxToolResultBytes || !matchesSchema(tool.outputSchema, result) {
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
	if len(result) > agent.MaxToolResultBytes || !matchesSchema(tool.outputSchema, result) {
		return nil, ErrResultInvalid
	}
	return result, nil
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

// PublicOnly drops every private-layer registration, for runs that may not
// open the private layer. The model never sees what it cannot use.
func PublicOnly(registrations []Registration) []Registration {
	kept := make([]Registration, 0, len(registrations))
	for _, registration := range registrations {
		if registration.Descriptor.Layer != LayerPrivate {
			kept = append(kept, registration)
		}
	}
	return kept
}
