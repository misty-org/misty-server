package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrInvalidDefinition = errors.New("invalid workflow definition")
	ErrProviderMissing   = errors.New("workflow provider missing")
	ErrCapabilityDenied  = errors.New("workflow capability denied")
)

type NodeDescriptor struct {
	Kind              string
	Version           int
	Capability        string
	Risk              Risk
	Location          ExecutionLocation
	InputSchema       JSONSchema
	OutputSchema      JSONSchema
	ToolSchema        JSONSchema
	TriggerSchema     JSONSchema
	EditorMetadata    map[string]any
	Idempotent        bool
	SupportsReconcile bool
	HealthCheck       func(context.Context) error
	Cancel            func(context.Context, Invocation) error
	Execute           func(context.Context, Invocation) (json.RawMessage, error)
	Reconcile         func(context.Context, Invocation) (json.RawMessage, bool, error)
}

// ProviderRegistration is the public provider contract used by server and
// device registries; NodeDescriptor is the engine-facing spelling.
type ProviderRegistration = NodeDescriptor

type Invocation struct {
	RunID          string
	NodeID         string
	Attempt        int
	IdempotencyKey string
	UserID         string
	SpaceID        string
	Config         json.RawMessage
	Input          json.RawMessage
}

type Registry struct {
	mu    sync.RWMutex
	nodes map[string]NodeDescriptor
}

func NewRegistry() *Registry { return &Registry{nodes: map[string]NodeDescriptor{}} }

func registryKey(kind string, version int) string { return fmt.Sprintf("%s@%d", kind, version) }

func (registry *Registry) Register(descriptor NodeDescriptor) error {
	if !validToken(descriptor.Kind, 120) || descriptor.Version < 1 || descriptor.Capability == "" || !validRisk(descriptor.Risk) || descriptor.Location != LocationCloud && descriptor.Location != LocationDevice && descriptor.Location != LocationEither || descriptor.Execute == nil || !validSchema(descriptor.InputSchema) || !validSchema(descriptor.OutputSchema) || descriptor.HealthCheck == nil || descriptor.Cancel == nil {
		return ErrInvalidDefinition
	}
	if descriptor.Risk != RiskRead && !descriptor.Idempotent && !descriptor.SupportsReconcile {
		return fmt.Errorf("%w: mutating provider %s must be idempotent or reconcilable", ErrInvalidDefinition, descriptor.Kind)
	}
	key := registryKey(descriptor.Kind, descriptor.Version)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.nodes[key]; exists {
		return fmt.Errorf("%w: duplicate provider %s", ErrInvalidDefinition, key)
	}
	registry.nodes[key] = descriptor
	return nil
}

func (registry *Registry) Resolve(kind string, version int) (NodeDescriptor, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	descriptor, ok := registry.nodes[registryKey(kind, version)]
	return descriptor, ok
}

func (registry *Registry) Descriptors() []NodeDescriptor {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	out := make([]NodeDescriptor, 0, len(registry.nodes))
	for _, descriptor := range registry.nodes {
		out = append(out, descriptor)
	}
	sort.Slice(out, func(i, j int) bool {
		return registryKey(out[i].Kind, out[i].Version) < registryKey(out[j].Kind, out[j].Version)
	})
	return out
}
