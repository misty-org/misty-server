package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func personalAgentRuntimeUsageKey(runID string) string {
	return "agent-runtime:" + runID + ":model:aggregate"
}

func aiInvocationRuntimeUsageKey(invocationID string) string {
	return "agent-runtime:" + invocationID + ":model:aggregate"
}

func (s *SpacesService) meterPersonalAgentRuntimeModel(ctx context.Context, run *db.SpaceRun, nodeID string, state workflowv2.StepState, raw json.RawMessage) error {
	if state == workflowv2.StepCompleted {
		return completeRuntimeModelTurn(ctx, s.usageMeter, run.RequestingMemberID, run.ID, nodeID, raw)
	}
	if state != workflowv2.StepRunning {
		return nil
	}
	if err := s.database.ReserveAgentModelTurn(ctx, run.OwnerUserID, run.ID, run.RuntimeRunID, nodeID); err != nil {
		return err
	}
	if s.usageMeter == nil {
		return nil
	}
	membership, err := s.database.AskExecutionContext(ctx, run.RequestingMemberID, run.SpaceID, run.AgentID)
	if err != nil {
		return err
	}
	membership = runtimeSnapshotMembership(run, membership)
	model := strings.TrimSpace(membership.ModelID)
	if model == "" {
		model = serveragent.InitialSelectedModelID
	}
	commandID, err := s.database.BillingCommandForRun(ctx, run.RequestingMemberID, run.ID)
	if err != nil {
		return err
	}
	provider, selectedModel, routeErr := s.runtimeModelBilling(ctx, run.OwnerUserID, run.ID, nodeID, model)
	if routeErr != nil {
		return routeErr
	}
	_, err = serveragent.ReserveMeasuredUsage(s.usageMeter, run.RequestingMemberID, "agent-runtime:"+run.ID+":model:"+nodeID, provider, selectedModel, runtimeAdmissionUnits(raw), commandID)
	return err
}

func (s *SpacesService) settlePersonalAgentRuntimeUsage(ctx context.Context, run *db.SpaceRun, status string, raw json.RawMessage) error {
	if s.usageMeter == nil || run == nil {
		return nil
	}
	if meter, ok := s.usageMeter.(interface {
		CompleteRuntime(context.Context, string, string, serveragent.ModelUsage) error
	}); ok {
		return meter.CompleteRuntime(ctx, run.RequestingMemberID, run.ID, agentRuntimeModelUsage(raw))
	}
	membership, err := s.database.AskExecutionContext(ctx, run.RequestingMemberID, run.SpaceID, run.AgentID)
	if err != nil {
		return err
	}
	membership = runtimeSnapshotMembership(run, membership)
	model := strings.TrimSpace(membership.ModelID)
	if model == "" {
		model = serveragent.InitialSelectedModelID
	}
	key := personalAgentRuntimeUsageKey(run.ID)
	reservation, err := serveragent.ReserveUsage(s.usageMeter, run.RequestingMemberID, run.SpaceID, key, db.CreditMeterAgentAI, agentRuntimeUsageProvider(), model, 0, serveragent.MaxModelOutputTokens)
	if err != nil {
		if status == "failed" {
			return nil
		}
		return err
	}
	usage := agentRuntimeModelUsage(raw)
	if status == "failed" || usage.Estimated {
		return s.usageMeter.Release(reservation)
	}
	_, err = s.usageMeter.Settle(reservation, key+":settle", db.CreditMeterAgentAI, agentRuntimeUsageProvider(), model, usage)
	return err
}

func (s *SpacesService) meterAIInvocationRuntimeModel(ctx context.Context, record *db.AIInvocationRecord, nodeID string, state string, raw json.RawMessage) error {
	if record != nil && state == "completed" {
		return completeRuntimeModelTurn(ctx, s.usageMeter, record.UserID, record.ID, nodeID, raw)
	}
	if record == nil || state != "running" {
		return nil
	}
	if err := s.database.ReserveAgentModelTurn(ctx, record.UserID, record.ID, record.RuntimeRunID, nodeID); err != nil {
		return err
	}
	if s.usageMeter == nil {
		return nil
	}
	modelID := aiInvocationMeteredModel(record)
	commandID := "agent-runtime:" + record.ID
	if record.Trigger == "schedule" {
		commandID = "agent-job:" + record.ID
	}
	provider, selectedModel, routeErr := s.runtimeModelBilling(ctx, record.UserID, record.ID, nodeID, modelID)
	if routeErr != nil {
		return routeErr
	}
	_, err := serveragent.ReserveMeasuredUsage(s.usageMeter, record.UserID, "agent-runtime:"+record.ID+":model:"+nodeID, provider, selectedModel, runtimeAdmissionUnits(raw), commandID)
	return err
}

func (s *SpacesService) settleAIInvocationRuntimeUsage(record *db.AIInvocationRecord, status string, raw json.RawMessage) error {
	if s.usageMeter == nil || record == nil {
		return nil
	}
	if meter, ok := s.usageMeter.(interface {
		CompleteRuntime(context.Context, string, string, serveragent.ModelUsage) error
	}); ok {
		return meter.CompleteRuntime(context.Background(), record.UserID, record.ID, agentRuntimeModelUsage(raw))
	}
	modelID := aiInvocationMeteredModel(record)
	key := aiInvocationRuntimeUsageKey(record.ID)
	reservation, err := serveragent.ReserveUsage(s.usageMeter, record.UserID, record.SpaceID, key, "assistant_ai", agentRuntimeUsageProvider(), modelID, 0, serveragent.MaxModelOutputTokens)
	if err != nil {
		if status == "failed" {
			return nil
		}
		return err
	}
	usage := agentRuntimeModelUsage(raw)
	if status == "failed" || usage.Estimated {
		return s.usageMeter.Release(reservation)
	}
	_, err = s.usageMeter.Settle(reservation, key+":settle", "assistant_ai", agentRuntimeUsageProvider(), modelID, usage)
	return err
}

func aiInvocationMeteredModel(record *db.AIInvocationRecord) string {
	if record != nil {
		var body aiInvocationInput
		if json.Unmarshal(record.RequestPayload, &body) == nil && strings.TrimSpace(body.ModelID) != "" {
			return strings.TrimSpace(body.ModelID)
		}
	}
	return serveragent.FrontierDefaultModelID()
}

func completeRuntimeModelTurn(ctx context.Context, meter serveragent.UsageMeter, account, run, node string, raw json.RawMessage) error {
	if m, ok := meter.(interface {
		CompleteRuntimeTurn(context.Context, string, string, string, serveragent.ModelUsage) error
	}); ok {
		return m.CompleteRuntimeTurn(ctx, account, run, node, agentRuntimeModelUsage(raw))
	}
	return nil
}

func runtimeAdmissionUnits(raw json.RawMessage) map[string]int64 {
	units := map[string]int64{"output_tokens": serveragent.MaxModelOutputTokens}
	var input struct {
		Bytes *int64 `json:"input_bytes"`
	}
	if json.Unmarshal(raw, &input) == nil && input.Bytes != nil && *input.Bytes >= 0 {
		units["input_bytes"] = *input.Bytes
	}
	return units
}

// Keep usage identity aligned with the actual instance provider.
func agentRuntimeUsageProvider() string {
	return "ai-gateway"
}
