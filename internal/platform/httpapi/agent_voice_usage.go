package api

import (
	"context"
	"github.com/google/uuid"
	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/billingadapter"
)

func (s *AgentsService) reserveAgentVoice(userID string, estimate agent.AgentVoiceUsage) (*billingadapter.Reservation, error) {
	operation := "agent.voice.transcription"
	if estimate.Model == agent.AgentSpeechModel {
		operation = "agent.voice.speech"
	}
	key := "agent-voice:" + uuid.NewString()
	return s.database.BillingService().Reserve(context.Background(), billingadapter.Request{Version: 1, AccountID: userID, Operation: operation, OperationID: key, Key: key, Usage: billingadapter.Usage{Provider: "openai", Model: estimate.Model, Units: map[string]int64{"audio_ms": estimate.DurationMS}, Estimated: true}})
}
func (s *AgentsService) settleAgentVoice(r *billingadapter.Reservation, usage agent.AgentVoiceUsage) error {
	return s.database.BillingService().Complete(context.Background(), "settle", r, r.Admission.Key+":settle", billingadapter.Usage{Provider: "openai", Model: usage.Model, Units: map[string]int64{"audio_ms": usage.DurationMS}}, "")
}
func (s *AgentsService) releaseAgentVoice(r *billingadapter.Reservation) error {
	return s.database.BillingService().Complete(context.Background(), "release", r, r.Admission.Key+":release", billingadapter.Usage{}, "")
}
