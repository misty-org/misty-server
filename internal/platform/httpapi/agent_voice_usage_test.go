package api

import (
	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"testing"
)

func TestVoiceBillingDisabledUsesNoWalletOrNetwork(t *testing.T) {
	service := NewAgentsService(&db.Database{})
	reservation, err := service.reserveAgentVoice("owner", agent.AgentVoiceUsage{Model: agent.AgentSpeechModel, DurationMS: 60000})
	if err != nil {
		t.Fatal(err)
	}
	if reservation.Admission.AccountID != "owner" || reservation.Admission.Operation != "agent.voice.speech" {
		t.Fatal(reservation)
	}
	if err = service.settleAgentVoice(reservation, agent.AgentVoiceUsage{Model: agent.AgentSpeechModel, DurationMS: 1000}); err != nil {
		t.Fatal(err)
	}
	if err = service.releaseAgentVoice(reservation); err != nil {
		t.Fatal(err)
	}
}
