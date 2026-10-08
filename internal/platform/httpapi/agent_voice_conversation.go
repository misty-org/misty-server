package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	agent "github.com/kannachi323/misty/server/internal/agents"
)

func (s *AgentsService) voiceConversationContext(ctx context.Context, user, conversation string) (string, error) {
	if _, err := s.database.AgentConversationIdentity(ctx, user, conversation); err != nil {
		return "", err
	}
	turns, err := s.database.AIConversationTurns(ctx, user, conversation)
	if err != nil {
		return "", err
	}
	if len(turns) > 8 {
		turns = turns[len(turns)-8:]
	}
	rows := []map[string]string{}
	for _, t := range turns {
		rows = append(rows, map[string]string{"user": voiceBoundText(t.Prompt, 1600), "assistant": voiceBoundText(t.Reply, 2400), "state": t.State, "invocation_id": t.InvocationID})
	}
	b, _ := json.Marshal(rows)
	return string(b), nil
}

func (s *AgentsService) runVoiceConversation(ctx context.Context, client *websocket.Conn, provider voiceProvider, operation, user, device, conversation, history string) {
	billing := s.database.BillingService()
	budget := &voiceReservations{operation: operation, user: user, model: voiceProviderModel(provider), reserve: billing.Reserve, estimate: voiceBillingEstimate(billing), disabled: !billing.Adapter.Enabled(), record: s.database.RecordVoiceUsage, complete: billing.Complete}
	boundTask := ""
	// Bind only this conversation's most recent non-voice task, never an ID
	// invented by the model. This also lets a new voice session steer existing work.
	turns, _ := s.database.AIConversationTurns(ctx, user, conversation)
	for _, t := range turns {
		if len(t.InvocationID) < 6 || t.InvocationID[:6] != "voice-" {
			boundTask = t.InvocationID
		}
	}
	status := func(ctx context.Context) (string, error) {
		// Recover an admission whose desktop receipt was lost on interruption.
		latest, err := s.database.AIConversationTurns(ctx, user, conversation)
		if err != nil {
			return "", err
		}
		for _, t := range latest {
			if !strings.HasPrefix(t.InvocationID, "voice-") {
				boundTask = t.InvocationID
			}
		}
		if boundTask == "" {
			return `{"state":"none"}`, nil
		}
		r, err := s.database.AIInvocationByID(ctx, user, boundTask)
		if err != nil {
			return "", err
		}
		if r.ConversationID != conversation {
			return "", errors.New("task conversation changed")
		}
		reply := ""
		if r.State == "completed" {
			reply, _ = s.ownedVoiceReply(ctx, user, boundTask)
		}
		b, _ := json.Marshal(map[string]string{"invocation_id": boundTask, "state": r.State, "reply": voiceBoundText(reply, 6000)})
		return string(b), nil
	}
	runConversationSession(ctx, client, provider, conversationHooks{
		Operation: operation, History: history,
		Access: func(ctx context.Context) error {
			if err := s.voiceAccess(ctx, user, device); err != nil {
				return err
			}
			_, err := s.database.AgentConversationIdentity(ctx, user, conversation)
			return err
		},
		Input: budget.input,
		Reserve: func(ctx context.Context, u agent.RealtimeVoiceUsage) error {
			return budget.admit(ctx, "conversation", u)
		},
		Settle: func(ctx context.Context, u agent.RealtimeVoiceUsage) error {
			err := budget.finish(ctx, u)
			if err == nil {
				budget.inputSeconds = 0
			}
			return err
		},
		Checkpoint: budget.checkpoint,
		Save: func(ctx context.Context, id, prompt, reply string, interrupted bool, started time.Time) error {
			return s.database.SaveVoiceConversationTurn(ctx, user, conversation, id, prompt, reply, interrupted, started)
		},
		Fail: func(ctx context.Context, id, prompt, message string, started time.Time) error {
			return s.database.SaveVoiceConversationFailure(ctx, user, conversation, id, prompt, message, started)
		},
		Tool: func(ctx context.Context, name string) (string, error) {
			if name == "get_context" {
				return s.voiceConversationContext(ctx, user, conversation)
			}
			return status(ctx)
		},
		TaskID: func() string { return boundTask },
		Result: func(ctx context.Context, id string) (string, error) {
			r, err := s.database.AIInvocationByID(ctx, user, id)
			if err != nil {
				return "", err
			}
			if r.ConversationID != conversation || r.State != "completed" {
				return "", errors.New("task result unavailable")
			}
			text, err := s.ownedVoiceReply(ctx, user, id)
			if err != nil {
				return "", err
			}
			boundTask = id
			b, _ := json.Marshal(map[string]string{"invocation_id": id, "state": "completed", "result": text})
			return string(b), nil
		},
		Bind: func(ctx context.Context, name, id, key string) (string, error) {
			if id == "" {
				return `{"error":"The task was not admitted. Ask the user to check the conversation before retrying."}`, nil
			}
			r, err := s.database.AIInvocationByID(ctx, user, id)
			if err != nil {
				return "", err
			}
			if r.ConversationID != conversation || (voiceToolAdmits(name) && r.IdempotencyKey != key) || (!voiceToolAdmits(name) && id != boundTask) {
				return "", errors.New("invalid voice task binding")
			}
			boundTask = id
			return status(ctx)
		},
	})
}

// voiceToolAdmits reports a voice tool whose client admits a new invocation
// under the call's idempotency key, rather than acting on the bound task.
func voiceToolAdmits(name string) bool {
	return name == "start_task" || name == "show_on_screen"
}
