package api

import (
	"context"

	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/workflow"
)

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
