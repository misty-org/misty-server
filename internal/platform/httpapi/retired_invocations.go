package api

import (
	"context"
	"encoding/json"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Retired automation records remain available for archival and reconciliation.
// They can reach a terminal state but never execute their former routine plan.
func (s *SpacesService) retireRemovedInvocation(ctx context.Context, record *db.AIInvocationRecord) error {
	if aiInvocationTerminal(record.State) {
		return nil
	}
	_, err := s.database.CommitAIInvocationEvent(ctx, record.UserID, record.ID, "retired-product:"+record.ID, "invocation.failed", json.RawMessage(`{"type":"invocation.failed","state":"failed","error":"This retired automation cannot resume. Its history has been preserved."}`), "failed")
	return err
}
