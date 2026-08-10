package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func (s *SpacesService) executeWorkflowV2(ctx context.Context, run *db.SpaceRun, agent *db.SpaceStudioResource, version *db.WorkflowVersion, prompt string) (*db.SpaceRun, error) {
	var definition workflowv2.Definition
	if json.Unmarshal(version.Definition, &definition) != nil {
		return s.finishFailedCanonicalRun(ctx, run, db.ErrSpaceInvalid)
	}
	registry := workflowv2.NewRegistry()
	toolProviders := map[string]workflowv2.NodeDescriptor{}
	declaredCapabilities := map[string]workflowv2.Risk{}
	for _, capability := range definition.Capabilities {
		declaredCapabilities[capability.Capability] = capability.Risk
	}
	for _, core := range workflowv2.CoreRegistry().Descriptors() {
		descriptor := core
		descriptor.Execute = func(ctx context.Context, invocation workflowv2.Invocation) (json.RawMessage, error) {
			return s.executeWorkflowNodeV2(ctx, run, agent, descriptor, invocation, prompt, toolProviders)
		}
		if descriptor.SupportsReconcile {
			descriptor.Reconcile = func(context.Context, workflowv2.Invocation) (json.RawMessage, bool, error) { return nil, false, nil }
		}
		if err := registry.Register(descriptor); err != nil {
			return s.finishFailedCanonicalRun(ctx, run, err)
		}
		if workflowToolEligible(descriptor, declaredCapabilities) {
			toolProviders["workflow."+descriptor.Kind] = descriptor
		}
	}
	resolver, err := s.workflowDependencyResolver(ctx, run, definition)
	if err != nil {
		return s.finishFailedCanonicalRun(ctx, run, err)
	}
	completedOutputs, err := s.database.CompletedWorkflowStepOutputs(ctx, run.RequestingMemberID, run.ID)
	if err != nil {
		return s.finishFailedCanonicalRun(ctx, run, err)
	}
	engine := workflowv2.Engine{
		Registry: registry,
		Resolver: resolver,
		Checkpoint: func(ctx context.Context, event workflowv2.StepEvent) error {
			return s.database.CheckpointWorkflowStep(ctx, run.ID, event)
		},
		Cooldown: func(ctx context.Context, _ workflowv2.StepEvent, seconds int) error {
			if seconds != 60 {
				return workflowv2.ErrInvalidDefinition
			}
			timer := time.NewTimer(time.Minute)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
		ItemCheckpoint: func(itemCtx context.Context, _ string, item json.RawMessage, result workflowv2.ExecutionResult, itemErr error) error {
			provider, eventID := TestingWorkflowEventIdentity(item)
			if provider == "" || eventID == "" {
				return nil
			}
			state := "completed"
			if itemErr != nil || result.State != workflowv2.RunCompleted {
				state = "failed"
			}
			return s.database.FinishWorkflowEventClaim(itemCtx, run.AgentInstanceID, run.WorkflowVersionID, provider, eventID, run.ID, state)
		},
	}
	// Bind the authorized tool catalog after registration so Agent-task nodes
	// use the same concrete providers, journaling, and permission context.
	for key, descriptor := range toolProviders {
		provider := descriptor
		provider.Execute = func(toolCtx context.Context, toolInvocation workflowv2.Invocation) (json.RawMessage, error) {
			return s.executeWorkflowNodeV2(toolCtx, run, agent, provider, toolInvocation, prompt, toolProviders)
		}
		toolProviders[key] = provider
	}
	result, err := engine.Execute(ctx, definition, workflowv2.ExecutionRequest{RunID: run.ID, UserID: run.RequestingMemberID, SpaceID: run.SpaceID, Input: run.Input, Completed: completedOutputs})
	if err != nil {
		if errors.Is(err, workflowv2.ErrAwaitingApproval) {
			return s.database.SpaceRun(ctx, run.RequestingMemberID, run.ID)
		}
		return s.finishFailedCanonicalRun(ctx, run, err)
	}
	serialized := TestingMustAPIRawJSON(map[string]any{"nodes": result.Outputs, "errors": result.Errors})
	return s.database.FinishSpaceRun(ctx, run.ID, string(result.State), serialized, "")
}

type apiWorkflowDependency struct {
	workflowID, checksum string
	definition           workflowv2.Definition
}

type apiWorkflowResolver map[string]apiWorkflowDependency

func (resolver apiWorkflowResolver) ResolveWorkflowVersion(versionID string) (string, string, workflowv2.Definition, bool) {
	item, ok := resolver[versionID]
	return item.workflowID, item.checksum, item.definition, ok
}

func (s *SpacesService) workflowDependencyResolver(ctx context.Context, run *db.SpaceRun, root workflowv2.Definition) (apiWorkflowResolver, error) {
	resolver := apiWorkflowResolver{}
	var load func(workflowv2.Definition) error
	load = func(definition workflowv2.Definition) error {
		for _, dependency := range definition.Dependencies {
			if _, exists := resolver[dependency.VersionID]; exists {
				continue
			}
			version, err := s.database.WorkflowVersion(ctx, run.RequestingMemberID, run.SpaceID, dependency.VersionID)
			if err != nil {
				return err
			}
			var child workflowv2.Definition
			if json.Unmarshal(version.Definition, &child) != nil {
				return db.ErrSpaceInvalid
			}
			resolver[dependency.VersionID] = apiWorkflowDependency{workflowID: version.WorkflowID, checksum: version.ChecksumSHA256, definition: child}
			if err := load(child); err != nil {
				return err
			}
		}
		return nil
	}
	return resolver, load(root)
}
