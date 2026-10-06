package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	agent "github.com/kannachi323/misty/server/internal/agents"
)

type fixtureVoiceProvider struct {
	events        chan agent.VoiceRealtimeEvent
	sent          chan map[string]any
	done          chan struct{}
	once          sync.Once
	limitOverride int
}


func (p *fixtureVoiceProvider) Configure() error { return nil }
func (p *fixtureVoiceProvider) SetOutputLimit(tokens int) error {
	if p.limitOverride != 0 {
		tokens = p.limitOverride
	}
	p.events <- agent.VoiceRealtimeEvent{Type: "session-updated", Raw: json.RawMessage(fmt.Sprintf(`{"session":{"max_output_tokens":%d,"audio":{"input":{"turn_detection":null}}}}`, tokens))}
	return nil
}
func (p *fixtureVoiceProvider) Close() { p.once.Do(func() { close(p.done) }) }
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






