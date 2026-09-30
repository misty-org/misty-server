package api

import (
	"context"
	"encoding/base64"
	"errors"
	"unicode/utf8"

	agent "github.com/kannachi323/misty/server/internal/agents"
)

type voiceClientEvent struct {
	Type         string `json:"type"`
	Audio        string `json:"audio,omitempty"`
	Sequence     int    `json:"sequence,omitempty"`
	InvocationID string `json:"invocation_id,omitempty"`
	AudioBytes   int    `json:"audio_bytes,omitempty"`
}
type voiceProvider interface {
	Read() (agent.VoiceRealtimeEvent, error)
	Send(any) error
	Configure() error
	Close()
}

type voiceSessionHooks struct {
	OperationID string
	Access      func(context.Context) error
	Reply       func(context.Context, string) (string, error)
	Checkpoint  func(context.Context, string, agent.RealtimeVoiceUsage) error
	Complete    func(context.Context, string, agent.RealtimeVoiceUsage) error
}

func (s *AgentsService) ownedVoiceReply(ctx context.Context, user, id string) (string, error) {
	if id == "" || len(id) > 160 {
		return "", errors.New("invalid invocation")
	}
	if _, err := s.database.AIInvocationByID(ctx, user, id); err != nil {
		return "", err
	}
	events, state, err := s.database.AIInvocationEvents(ctx, user, id, 0)
	if err != nil {
		return "", err
	}
	text, err := agentSpeechText(state, events)
	if err != nil {
		return "", err
	}
	// Bound token admission even for four-byte characters; full text stays in
	// the conversation. The voice estimate also covers the fixed instructions.
	if len(text) > 10000 {
		text = text[:10000]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		text += ". The rest of my answer is in the conversation."
	}
	return text, nil
}

// Pure input invariants also exercised without provider/database connections.
type voiceSessionState struct {
	Ready, Committed, Transcribed, Responding bool
	Sequence, AudioBytes, OutputBytes         int
	ItemID, ResponseID                        string
}

func (s *voiceSessionState) Append(sequence int, encoded string) ([]byte, error) {
	if s.Committed || s.Responding || sequence != s.Sequence || len(encoded) > 64000 {
		return nil, errors.New("Voice audio arrived out of order.")
	}
	pcm, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(pcm) == 0 || len(pcm)%2 != 0 || s.AudioBytes+len(pcm) > 24000*2*60 {
		return nil, errors.New("Voice audio is invalid or too long.")
	}
	s.Sequence++
	s.AudioBytes += len(pcm)
	return pcm, nil
}
func (s *voiceSessionState) Commit() error {
	if s.Committed || s.Responding || s.AudioBytes < 7200 {
		return errors.New("Hold the shortcut long enough to say your question.")
	}
	s.Committed = true
	return nil
}
