package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/workflow"
)

// runModelUser is whose models run a Space run. Work someone asked of another
// person's agent runs on the models of the person who asked.
func runModelUser(run *db.SpaceRun) string {
	if run.ModelUserID != "" {
		return run.ModelUserID
	}
	return run.RequestingMemberID
}

// admitPersonalAgentRuntimeTurn counts one model turn against the run's turn
// limit.
func (s *SpacesService) admitPersonalAgentRuntimeTurn(ctx context.Context, run *db.SpaceRun, nodeID string, state workflow.StepState) error {
	if state != workflow.StepRunning {
		return nil
	}
	return s.database.ReserveAgentModelTurn(ctx, run.OwnerUserID, run.ID, run.RuntimeRunID, nodeID)
}

// admitAIInvocationRuntimeTurn counts one model turn of an invocation against
// its turn limit.
func (s *SpacesService) admitAIInvocationRuntimeTurn(ctx context.Context, record *db.AIInvocationRecord, nodeID string, state string) error {
	if record == nil || state != "running" {
		return nil
	}
	return s.database.ReserveAgentModelTurn(ctx, record.UserID, record.ID, record.RuntimeRunID, nodeID)
}

// invocationRecordModel is the model an invocation asked for, else the default.
func invocationRecordModel(record *db.AIInvocationRecord) string {
	if record != nil {
		var body aiInvocationInput
		if json.Unmarshal(record.RequestPayload, &body) == nil && strings.TrimSpace(body.ModelID) != "" {
			return strings.TrimSpace(body.ModelID)
		}
	}
	return agent.FrontierDefaultModelID()
}
