package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/billingadapter"
)

type conversationHooks struct {
	Operation, History string
	Access             func(context.Context) error
	Input              func(context.Context, int) error
	Reserve, Settle    func(context.Context, agent.RealtimeVoiceUsage) error
	Checkpoint         func(context.Context, string, agent.RealtimeVoiceUsage) error
	Save               func(context.Context, string, string, string, bool, time.Time) error
	Fail               func(context.Context, string, string, string, time.Time) error
	Tool               func(context.Context, string) (string, error)
	TaskID             func() string
	Bind               func(context.Context, string, string, string) (string, error)
	Result             func(context.Context, string) (string, error)
}
type conversationClientEvent struct {
	Type, Audio, Text, CallID, InvocationID, ItemID string
	Sequence, AudioEndMs                            int
}
type conversationTool struct {
	ID, Name, Instruction, Key string
}

func voiceBoundText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func parseConversationTool(e agent.VoiceRealtimeEvent) (conversationTool, error) {
	t := conversationTool{ID: e.CallID, Name: e.Name}
	if t.ID == "" || len(t.ID) > 100 || len(e.Arguments) > 5000 {
		return t, errors.New("invalid voice tool")
	}
	var args struct {
		Instruction string `json:"instruction"`
	}
	d := json.NewDecoder(strings.NewReader(e.Arguments))
	d.DisallowUnknownFields()
	if d.Decode(&args) != nil {
		return t, errors.New("invalid voice tool arguments")
	}
	t.Instruction = strings.TrimSpace(args.Instruction)
	switch t.Name {
	case "get_context", "get_task_status", "cancel_task":
		if t.Instruction != "" {
			return t, errors.New("unexpected voice tool arguments")
		}
	case "start_task", "show_on_screen", "steer_task":
		if t.Instruction == "" || len(t.Instruction) > 4000 {
			return t, errors.New("missing voice instruction")
		}
	default:
		return t, errors.New("unsupported voice tool")
	}
	return t, nil
}

// conversationSession is one spoken conversation. Its run loop is the only
// writer to the client, the provider and billing.
type conversationSession struct {
	ctx      context.Context
	client   *websocket.Conn
	provider voiceProvider
	h        conversationHooks
	up       chan agent.VoiceRealtimeEvent

	usage                                                   agent.RealtimeVoiceUsage
	ready, busy, responding, transcribed, interrupted       bool
	playbackDone, responseDone, resultOnly, closed          bool
	input                                                   voiceSessionState
	turns, totalInput, outputBytes, contextBytes, toolCount int
	lastHistory, prompt, reply, turnID, currentItem         string
	failureMessage                                          string
	started, committed, deadline                            time.Time
	pending                                                 *conversationTool
	seen                                                    map[string]bool
}

// One actor owns all provider/client writes and billing. No microphone is open
// while idle. A socket is deliberately not reconnectable: uncertain actions
// remain in the canonical invocation journal and must never be replayed.
func runConversationSession(parent context.Context, client *websocket.Conn, provider voiceProvider, h conversationHooks) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	client.SetReadLimit(100 << 10)
	in := make(chan conversationClientEvent, 16)
	s := &conversationSession{
		ctx: ctx, client: client, provider: provider, h: h, up: make(chan agent.VoiceRealtimeEvent, 32),
		usage: agent.RealtimeVoiceUsage{}, seen: map[string]bool{}, lastHistory: h.History,
		contextBytes: len(h.History) + 7000, // includes instructions, tools and protocol overhead
		deadline:     time.Now().Add(20 * time.Second),
	}
	clientGone, providerGone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(clientGone)
		for {
			var e conversationClientEvent
			if client.ReadJSON(&e) != nil {
				return
			}
			select {
			case in <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		defer close(providerGone)
		for {
			e, err := provider.Read()
			if err != nil {
				return
			}
			select {
			case s.up <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	defer s.settleOnExit()
	if err := provider.Send(agent.VoiceConversationConfig(h.History)); err != nil {
		s.fail(err)
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var err error
		select {
		case <-ctx.Done():
			if !s.busy {
				_ = s.write(map[string]string{"type": "session.expired"})
			}
			return
		case <-clientGone:
			s.drainAfterDisconnect(providerGone)
			return
		case <-providerGone:
			s.fail(errors.New("voice provider disconnected"))
			return
		case <-ticker.C:
			if time.Now().After(s.deadline) {
				if !s.busy {
					_ = s.write(map[string]string{"type": "session.expired"})
				} else {
					s.fail(errors.New("voice turn timed out"))
				}
				return
			}
		case e := <-in:
			var end bool
			if end, err = s.handleClient(e); end {
				return
			}
		case e := <-s.up:
			err = s.handleUpstream(e)
		}
		if err != nil {
			s.fail(err)
			return
		}
		if s.turns >= 8 && !s.busy {
			_ = s.write(map[string]string{"type": "session.expired"})
			return
		}
	}
}

func (s *conversationSession) write(v any) error {
	if s.closed {
		return nil
	}
	_ = s.client.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return s.client.WriteJSON(v)
}

func (s *conversationSession) fail(err error) {
	log.Printf("voice_conversation_failed operation_id=%q error=%q", s.h.Operation, err)
	s.failureMessage = "The voice session stopped. Your tasks and saved conversation are still available. Try voice again."
	if errors.Is(err, billingadapter.ErrDenied) {
		s.failureMessage = "Voice could not start because billing did not authorize this response. Check Billing before retrying. Existing tasks remain available."
	}
	_ = s.write(map[string]string{"type": "error", "message": s.failureMessage})
}

// settleOnExit keeps unmeasured provider work's durable reservation for
// recovery and saves an interrupted turn.
func (s *conversationSession) settleOnExit() {
	c, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()
	if s.responding || (s.input.Committed && !s.transcribed) {
		_ = s.h.Checkpoint(c, "reconcile", s.usage)
	} else {
		_ = s.h.Settle(c, s.usage)
	}
	if s.busy && s.prompt != "" {
		if s.failureMessage != "" && s.h.Fail != nil {
			if err := s.h.Fail(c, s.turnID, s.prompt, s.failureMessage, s.started); err != nil {
				log.Printf("voice_failure_persistence_failed operation_id=%q error=%q", s.h.Operation, err)
			}
		} else {
			_ = s.h.Save(c, s.turnID, s.prompt, "[Voice reply interrupted; unfinished speech omitted.]", true, s.started)
		}
	}
}

// drainAfterDisconnect measures usage briefly after the client leaves rather
// than releasing money for generation already in flight. It never executes
// pending tools.
func (s *conversationSession) drainAfterDisconnect(providerGone <-chan struct{}) {
	s.closed = true
	if s.responding {
		_ = s.provider.Send(map[string]string{"type": "response-cancel"})
	}
	until := time.NewTimer(3 * time.Second)
	defer until.Stop()
	for s.responding || (s.input.Committed && !s.transcribed) {
		select {
		case event := <-s.up:
			if event.Type == "response-done" {
				if u, err := agent.RealtimeResponseUsage(event.Raw); err == nil {
					s.usage.Add(u)
					s.responding = false
				}
			}
			if event.Type == "input-transcription-completed" {
				if u, err := agent.RealtimeTranscriptionUsage(event.Raw); err == nil {
					s.usage.Add(u)
					s.transcribed = true
					s.prompt = event.Transcript
				}
			}
		case <-until.C:
			return
		case <-providerGone:
			return
		}
	}
}
