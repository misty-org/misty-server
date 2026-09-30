package api

import (
	"context"

	"github.com/gorilla/websocket"
	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/billingadapter"
)

func (s *AgentsService) runVoiceRealtime(ctx context.Context, client *websocket.Conn, provider voiceProvider, reservation *billingadapter.Reservation, user, device string) {
	runVoiceSession(ctx, client, provider, voiceSessionHooks{
		OperationID: reservation.Admission.OperationID,
		Access:      func(ctx context.Context) error { return s.voiceAccess(ctx, user, device) },
		Reply: func(ctx context.Context, id string) (string, error) {
			if err := s.voiceAccess(ctx, user, device); err != nil {
				return "", err
			}
			return s.ownedVoiceReply(ctx, user, id)
		},
		Checkpoint: func(ctx context.Context, state string, usage agent.RealtimeVoiceUsage) error {
			return s.database.RecordVoiceUsage(ctx, reservation, state, usage)
		},
		Complete: func(ctx context.Context, action string, usage agent.RealtimeVoiceUsage) error {
			return s.database.BillingService().Complete(ctx, action, reservation, reservation.Admission.Key+":"+action,
				billingadapter.Usage{Provider: "openai", Model: agent.AgentRealtimeModel, Units: usage}, "")
		},
	})
}
