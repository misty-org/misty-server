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

func (s *SpacesService) meterPersonalAgentRuntimeModel(ctx context.Context, run *db.SpaceRun, nodeID string, state workflowv2.StepState, _ json.RawMessage) error {
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
	_, err = serveragent.ReserveUsage(s.usageMeter, run.RequestingMemberID, run.SpaceID, personalAgentRuntimeUsageKey(run.ID), db.CreditMeterAgentAI, "ai-gateway", model, 32_000, serveragent.MaxModelOutputTokens)
	return err
}

func (s *SpacesService) settlePersonalAgentRuntimeUsage(ctx context.Context, run *db.SpaceRun, status string, raw json.RawMessage) error {
	if s.usageMeter == nil || run == nil {
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
	key := personalAgentRuntimeUsageKey(run.ID)
	reservation, err := serveragent.ReserveUsage(s.usageMeter, run.RequestingMemberID, run.SpaceID, key, db.CreditMeterAgentAI, "ai-gateway", model, 32_000, serveragent.MaxModelOutputTokens)
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
	_, err = s.usageMeter.Settle(reservation, key+":settle", db.CreditMeterAgentAI, "ai-gateway", model, usage)
	return err
}

func (s *SpacesService) meterAIInvocationRuntimeModel(ctx context.Context, record *db.AIInvocationRecord, nodeID string, state string, _ json.RawMessage) error {
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
	_, err := serveragent.ReserveUsage(s.usageMeter, record.UserID, record.SpaceID, aiInvocationRuntimeUsageKey(record.ID), "assistant_ai", "ai-gateway", modelID, 32_000, serveragent.MaxModelOutputTokens)
	return err
}

func (s *SpacesService) settleAIInvocationRuntimeUsage(record *db.AIInvocationRecord, status string, raw json.RawMessage) error {
	if s.usageMeter == nil || record == nil {
		return nil
	}
	modelID := aiInvocationMeteredModel(record)
	key := aiInvocationRuntimeUsageKey(record.ID)
	reservation, err := serveragent.ReserveUsage(s.usageMeter, record.UserID, record.SpaceID, key, "assistant_ai", "ai-gateway", modelID, 32_000, serveragent.MaxModelOutputTokens)
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
	_, err = s.usageMeter.Settle(reservation, key+":settle", "assistant_ai", "ai-gateway", modelID, usage)
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
