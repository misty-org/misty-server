package api

import (
	"context"
	"strings"

	"github.com/misty-org/misty-server/internal/agent"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// A long chat's older turns become running notes. When agents run on desktops
// the server calls no model, so a run's context names the turns that just aged
// out and the desktop writes the notes on its fast model as the run ends. The
// server works out the same plan again before it saves them, so it never
// relies on the desktop for which turns the notes cover.

type deviceSummaryRequest struct {
	Previous string `json:"previous"`
	Turns    string `json:"turns"`
}

func (s *SpacesService) deviceSummaryPlan(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput, modelID string) (*db.AIConversationSummary, conversationHistoryPlan, bool) {
	if record.ConversationID == "" || body.Mode == "companion" || teachTurn(body) {
		return nil, conversationHistoryPlan{}, false
	}
	turns, err := s.database.AIConversationTurns(ctx, record.UserID, record.ConversationID)
	if err != nil {
		return nil, conversationHistoryPlan{}, false
	}
	summary, err := s.database.AIConversationSummary(ctx, record.UserID, record.ConversationID)
	if err != nil {
		summary = nil
	}
	plan := planConversationHistory(turns, record.ID, summary, conversationHistoryBudget(agent.ModelContextWindow(ctx, modelID)))
	return summary, plan, len(plan.summarize) > 0
}

// deviceSummaryRequest is what the desktop summarizes, or nil when no turns aged out.
func (s *SpacesService) deviceSummaryRequest(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput, modelID string) *deviceSummaryRequest {
	summary, plan, due := s.deviceSummaryPlan(ctx, record, body, modelID)
	if !due {
		return nil
	}
	request := &deviceSummaryRequest{Turns: renderConversationTurns(plan.summarize)}
	if summary != nil {
		request.Previous = summary.Summary
	}
	return request
}

// saveDeviceSummary keeps the notes a desktop wrote for the turns that aged out.
func (s *SpacesService) saveDeviceSummary(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput, modelID, notes string) {
	notes = strings.TrimSpace(notes)
	_, plan, due := s.deviceSummaryPlan(ctx, record, body, modelID)
	if notes == "" || !due {
		return
	}
	last := plan.summarize[len(plan.summarize)-1]
	_ = s.database.SaveAIConversationSummary(ctx, record.UserID, db.AIConversationSummary{
		ConversationID: record.ConversationID, ThroughInvocationID: last.InvocationID, ThroughCreatedAt: last.CreatedAt,
		SummarizedTurns: plan.covered + len(plan.summarize), Summary: truncateAgentRuntimeText(notes, 40_000), Model: "device:" + modelID,
	})
}
