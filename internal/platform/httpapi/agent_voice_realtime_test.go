package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	agent "github.com/kannachi323/misty/server/internal/agents"
)

type fixtureVoiceProvider struct {
	events chan agent.VoiceRealtimeEvent
	sent   chan map[string]any
	done   chan struct{}
	once   sync.Once
	rtc    bool
}

func (p *fixtureVoiceProvider) WebRTC() bool { return p.rtc }

func (p *fixtureVoiceProvider) Configure() error { return nil }
func (p *fixtureVoiceProvider) Close()           { p.once.Do(func() { close(p.done) }) }
func (p *fixtureVoiceProvider) Read() (agent.VoiceRealtimeEvent, error) {
	select {
	case event := <-p.events:
		return event, nil
	case <-p.done:
		return agent.VoiceRealtimeEvent{}, io.EOF
	}
}
func (p *fixtureVoiceProvider) Send(value any) error {
	raw, _ := json.Marshal(value)
	var event map[string]any
	_ = json.Unmarshal(raw, &event)
	p.sent <- event
	return nil
}

type voiceSettlement struct {
	Action string
	Usage  agent.RealtimeVoiceUsage
}
type voiceFixture struct {
	client   *websocket.Conn
	provider *fixtureVoiceProvider
	settled  chan voiceSettlement
	states   chan string
	finished chan struct{}
}

func newVoiceFixture(t *testing.T, rtc ...bool) *voiceFixture {
	t.Helper()
	f := &voiceFixture{provider: &fixtureVoiceProvider{events: make(chan agent.VoiceRealtimeEvent, 16), sent: make(chan map[string]any, 16), done: make(chan struct{}), rtc: len(rtc) > 0 && rtc[0]}, settled: make(chan voiceSettlement, 1), states: make(chan string, 16), finished: make(chan struct{})}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		defer f.provider.Close()
		defer close(f.finished)
		runVoiceSession(r.Context(), conn, f.provider, voiceSessionHooks{
			OperationID: "fixture", Access: func(context.Context) error { return nil },
			Reply: func(_ context.Context, id string) (string, error) {
				if id != "owned-completed" {
					return "", errors.New("not owned or incomplete")
				}
				return "Blue lantern.", nil
			},
			Checkpoint: func(_ context.Context, state string, _ agent.RealtimeVoiceUsage) error { f.states <- state; return nil },
			Complete: func(_ context.Context, action string, usage agent.RealtimeVoiceUsage) error {
				copied := agent.RealtimeVoiceUsage{}
				copied.Add(usage)
				f.settled <- voiceSettlement{action, copied}
				return nil
			},
		})
	}))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f.client = conn
	t.Cleanup(func() { conn.Close(); f.provider.Close(); server.Close() })
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "session-updated", Raw: json.RawMessage(`{"session":{"audio":{"input":{"turn_detection":null}}}}`)}
	if f.provider.rtc {
		select {
		case e := <-f.provider.sent:
			if e["type"] != "input-audio-clear" {
				t.Fatal(e)
			}
		case <-time.After(time.Second):
			t.Fatal("WebRTC input not cleared")
		}
		f.provider.events <- agent.VoiceRealtimeEvent{Type: "audio-cleared"}
	}
	if f.read(t)["type"] != "ready" {
		t.Fatal("not ready")
	}
	return f
}
func (f *voiceFixture) read(t *testing.T) map[string]any {
	t.Helper()
	_ = f.client.SetReadDeadline(time.Now().Add(3 * time.Second))
	var event map[string]any
	if err := f.client.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	return event
}
func (f *voiceFixture) send(t *testing.T, event voiceClientEvent) {
	t.Helper()
	if err := f.client.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
}
func (f *voiceFixture) committed(t *testing.T) {
	t.Helper()
	f.send(t, voiceClientEvent{Type: "audio.append", Audio: base64.StdEncoding.EncodeToString(make([]byte, 7200))})
	f.send(t, voiceClientEvent{Type: "audio.commit"})
	for _, expected := range []string{"input-audio-append", "input-audio-commit"} {
		select {
		case e := <-f.provider.sent:
			if e["type"] != expected {
				t.Fatal(e)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("missing provider input")
		}
	}
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "audio-committed", ItemID: "audio1"}
}
func (f *voiceFixture) transcript() {
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "input-transcription-completed", ItemID: "audio1", Transcript: "What is the label?", Raw: json.RawMessage(`{"usage":{"type":"tokens","input_tokens":15,"output_tokens":10}}`)}
}
func (f *voiceFixture) respond(t *testing.T) {
	t.Helper()
	f.send(t, voiceClientEvent{Type: "reply", InvocationID: "owned-completed"})
	for _, expected := range []string{"conversation-item-create", "response-create"} {
		select {
		case e := <-f.provider.sent:
			if e["type"] != expected {
				t.Fatal(e)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("missing reply")
		}
	}
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "response-created", ResponseID: "reply1"}
}
func (f *voiceFixture) responseDone(status string) {
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "response-done", ResponseID: "reply1", Status: status, Raw: json.RawMessage(`{"response":{"usage":{"input_tokens":15,"output_tokens":10,"input_token_details":{"text_tokens":10,"audio_tokens":5,"cached_tokens":0},"output_token_details":{"text_tokens":4,"audio_tokens":6}}}}`)}
}

func TestRealtimeVoiceRoutesFinalInputAndOnlyOwnedCompletedReply(t *testing.T) {
	f := newVoiceFixture(t)
	f.committed(t)
	f.transcript()
	if e := f.read(t); e["type"] != "transcript" {
		t.Fatal(e)
	}
	if len(f.provider.sent) != 0 {
		t.Fatal("voice generated before backend result")
	}
	f.respond(t)
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "audio-delta", ResponseID: "reply1", Delta: "AAAAAA=="}
	f.responseDone("completed")
	if e := f.read(t); e["type"] != "audio" {
		t.Fatal(e)
	}
	if e := f.read(t); e["type"] != "done" {
		t.Fatal(e)
	}
	result := <-f.settled
	if result.Action != "settle" || result.Usage["transcription_input_tokens"] != 15 || result.Usage["output_audio_tokens"] != 6 {
		t.Fatal(result)
	}
}

func TestRealtimeVoiceRejectsForeignOrIncompleteReplyWithoutProviderWork(t *testing.T) {
	f := newVoiceFixture(t)
	f.send(t, voiceClientEvent{Type: "reply", InvocationID: "foreign"})
	if e := f.read(t); e["type"] != "error" {
		t.Fatal(e)
	}
	if r := <-f.settled; r.Action != "release" {
		t.Fatal(r)
	}
	if len(f.provider.sent) != 0 {
		t.Fatal("unowned reply reached provider")
	}
}

func TestRealtimeCancellationSettlesConsumedTranscription(t *testing.T) {
	f := newVoiceFixture(t)
	f.committed(t)
	f.client.Close()
	f.transcript()
	select {
	case r := <-f.settled:
		if r.Action != "settle" || r.Usage["transcription_input_tokens"] != 15 {
			t.Fatal(r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled usage lost")
	}
}

func TestRealtimeCancellationRequestsProviderCancelAndSettlesPartialOutput(t *testing.T) {
	f := newVoiceFixture(t)
	f.respond(t)
	f.client.Close()
	select {
	case e := <-f.provider.sent:
		if e["type"] != "response-cancel" {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("provider not cancelled")
	}
	f.responseDone("cancelled")
	select {
	case r := <-f.settled:
		if r.Action != "settle" || r.Usage["output_audio_tokens"] != 6 {
			t.Fatal(r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("partial usage lost")
	}
}

func TestRealtimeMissingUsageRetainsHoldForReconciliation(t *testing.T) {
	f := newVoiceFixture(t)
	f.committed(t)
	f.provider.Close()
	select {
	case <-f.finished:
	case <-time.After(3 * time.Second):
		t.Fatal("session did not finish")
	}
	if len(f.settled) != 0 {
		t.Fatal("unknown provider work settled or released")
	}
	found := false
	for len(f.states) > 0 {
		found = (<-f.states) == "reconcile" || found
	}
	if !found {
		t.Fatal("no durable reconciliation marker")
	}
}

func TestRealtimeInputDoesNotAllowDuplicateOrOversizedTurns(t *testing.T) {
	s := voiceSessionState{}
	pcm := base64.StdEncoding.EncodeToString(make([]byte, 7200))
	if _, err := s.Append(1, pcm); err == nil {
		t.Fatal("out of order accepted")
	}
	if _, err := s.Append(0, pcm); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err == nil {
		t.Fatal("duplicate commit")
	}
	if _, err := s.Append(1, pcm); err == nil {
		t.Fatal("audio after commit")
	}
	if voiceTicketHash("fixture") == "fixture" || voiceTicketHash("fixture") == voiceTicketHash("different") {
		t.Fatal("ticket hashing")
	}
}
