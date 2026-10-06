package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func conversationFixture(t *testing.T, customize func(*conversationHooks)) *voiceFixture {
	t.Helper()
	f := &voiceFixture{provider: &fixtureVoiceProvider{events: make(chan agent.VoiceRealtimeEvent, 32), sent: make(chan map[string]any, 32), done: make(chan struct{})}, finished: make(chan struct{})}
	h := conversationHooks{Operation: "realtime-voice:fixture", History: "[]", Access: func(context.Context) error { return nil }, Input: func(context.Context, int) error { return nil }, Reserve: func(context.Context, agent.RealtimeVoiceUsage) error { return nil }, Settle: func(context.Context, agent.RealtimeVoiceUsage) error { return nil }, Checkpoint: func(context.Context, string, agent.RealtimeVoiceUsage) error { return nil }, Save: func(context.Context, string, string, string, bool, time.Time) error { return nil }, Tool: func(_ context.Context, name string) (string, error) {
		if name == "get_context" {
			return "[]", nil
		}
		return `{"state":"none"}`, nil
	}, TaskID: func() string { return "bound-task" }, Bind: func(_ context.Context, name, id, key string) (string, error) {
		if id != "bound-task" {
			return "", errors.New("foreign")
		}
		return `{"state":"running"}`, nil
	}}
	if customize != nil {
		customize(&h)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		defer f.provider.Close()
		defer close(f.finished)
		runConversationSession(r.Context(), c, f.provider, h)
	}))
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f.client = c
	t.Cleanup(func() { c.Close(); f.provider.Close(); server.Close() })
	voiceSent(t, f, "session-update")
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "session-updated", Raw: json.RawMessage(`{"session":{"max_output_tokens":768,"audio":{"input":{"turn_detection":null}}}}`)}
	if e := f.read(t); e["type"] != "ready" {
		t.Fatal(e)
	}
	return f
}
func conversationSend(t *testing.T, f *voiceFixture, e any) {
	t.Helper()
	if err := f.client.WriteJSON(e); err != nil {
		t.Fatal(err)
	}
}
func conversationBegin(t *testing.T, f *voiceFixture) {
	t.Helper()
	conversationSend(t, f, map[string]string{"type": "turn.begin"})
	if e := f.read(t); e["type"] != "turn.ready" {
		t.Fatal(e)
	}
}
func conversationText(t *testing.T, f *voiceFixture) {
	t.Helper()
	conversationBegin(t, f)
	conversationSend(t, f, map[string]string{"type": "text.commit", "text": "Hello"})
	if e := f.read(t); e["type"] != "transcript" {
		t.Fatal(e)
	}
	voiceSent(t, f, "conversation-item-create")
	voiceSent(t, f, "response-create")
}
func conversationComplete(t *testing.T, f *voiceFixture) {
	t.Helper()
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "audio-transcript-delta", Delta: "Hello there."}
	f.responseDone("completed")
	if e := f.read(t); e["type"] != "audio.done" {
		t.Fatal(e)
	}
	conversationSend(t, f, map[string]string{"type": "playback.done"})
	if e := f.read(t); e["type"] != "turn.done" {
		t.Fatal(e)
	}
}

func TestVoiceConversationRespondsBeforeTranscriptionAndReusesConnection(t *testing.T) {
	saved := make(chan string, 10)
	f := conversationFixture(t, func(h *conversationHooks) {
		h.Save = func(_ context.Context, _, prompt, reply string, _ bool, _ time.Time) error {
			saved <- prompt + ":" + reply
			return nil
		}
	})
	conversationBegin(t, f)
	conversationSend(t, f, map[string]any{"type": "audio.append", "sequence": 0, "audio": base64.StdEncoding.EncodeToString(make([]byte, 7200))})
	conversationSend(t, f, map[string]string{"type": "audio.commit"})
	voiceSent(t, f, "input-audio-append")
	voiceSent(t, f, "input-audio-commit")
	voiceSent(t, f, "response-create")
	// There is deliberately no transcription result yet. Audio can already play.
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "audio-delta", ItemID: "speech", Delta: "AAAAAA=="}
	if e := f.read(t); e["type"] != "audio" {
		t.Fatal(e)
	}
	f.transcript()
	if e := f.read(t); e["type"] != "transcript" {
		t.Fatal(e)
	}
	if p := <-saved; !strings.HasPrefix(p, "What is the label?") {
		t.Fatal(p)
	}
	conversationComplete(t, f)
	conversationText(t, f)
	conversationComplete(t, f)
}
func TestVoiceConversationDenialNeverStartsResponse(t *testing.T) {
	failed := make(chan string, 1)
	f := conversationFixture(t, func(h *conversationHooks) {
		h.Reserve = func(context.Context, agent.RealtimeVoiceUsage) error { return billingadapter.ErrDenied }
		h.Fail = func(_ context.Context, _, prompt, message string, _ time.Time) error {
			failed <- prompt + ": " + message
			return nil
		}
		h.Save = func(_ context.Context, _, _, reply string, _ bool, _ time.Time) error {
			if reply != "" {
				t.Error("failure saved as successful speech")
			}
			return nil
		}
	})
	conversationBegin(t, f)
	conversationSend(t, f, map[string]string{"type": "text.commit", "text": "Hi"})
	if e := f.read(t); e["type"] != "transcript" {
		t.Fatal(e)
	}
	voiceSent(t, f, "conversation-item-create")
	if e := f.read(t); e["type"] != "error" || !strings.Contains(e["message"].(string), "billing did not authorize") {
		t.Fatal(e)
	}
	voiceFinished(t, f)
	select {
	case message := <-failed:
		if !strings.Contains(message, "Hi: Voice could not start because billing") {
			t.Fatal(message)
		}
	default:
		t.Fatal("missing durable failure")
	}
	if len(f.provider.sent) != 0 {
		t.Fatal("unfunded generation")
	}
}
func TestVoiceConversationToolsWaitForDurableInputAndVerifyBinding(t *testing.T) {
	settled := make(chan struct{}, 8)
	f := conversationFixture(t, func(h *conversationHooks) {
		h.Settle = func(context.Context, agent.RealtimeVoiceUsage) error { settled <- struct{}{}; return nil }
	})
	conversationBegin(t, f)
	conversationSend(t, f, map[string]any{"type": "audio.append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 7200))})
	conversationSend(t, f, map[string]string{"type": "audio.commit"})
	voiceSent(t, f, "input-audio-append")
	voiceSent(t, f, "input-audio-commit")
	voiceSent(t, f, "response-create")
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "function-call-arguments-done", CallID: "call-1", Name: "start_task", Arguments: `{"instruction":"Describe my screen without changes"}`}
	f.responseDone("completed")
	f.transcript()
	if e := f.read(t); e["type"] != "transcript" {
		t.Fatal(e)
	}
	e := f.read(t)
	if e["type"] != "tool.call" || e["key"] != "voice-fixture-1:call-1" || e["instruction"] != "Describe my screen without changes" {
		t.Fatal(e)
	}
	select {
	case <-settled:
	default:
		t.Fatal("tool dispatched before releasing the measured voice hold")
	}
	conversationSend(t, f, map[string]string{"type": "tool.result", "callId": "call-1", "invocationId": "foreign"})
	if e := f.read(t); e["type"] != "error" {
		t.Fatal(e)
	}
	voiceFinished(t, f)
	if len(f.provider.sent) != 0 {
		t.Fatal("foreign result reached provider")
	}
}
func TestVoiceConversationInterruptTruncatesAndDoesNotExecuteTool(t *testing.T) {
	f := conversationFixture(t, nil)
	conversationText(t, f)
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "audio-delta", ItemID: "speech", Delta: "AAAAAA=="}
	f.read(t)
	conversationSend(t, f, map[string]any{"type": "turn.cancel", "itemId": "speech", "audioEndMs": 0})
	voiceSent(t, f, "response-cancel")
	voiceSent(t, f, "conversation-item-truncate")
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "function-call-arguments-done", CallID: "late", Name: "start_task", Arguments: `{"instruction":"delete"}`}
	f.responseDone("canceled")
	e := f.read(t)
	if e["type"] != "turn.done" || e["interrupted"] != true || strings.Contains(e["reply"].(string), "delete") {
		t.Fatal(e)
	}
	conversationText(t, f)
	conversationComplete(t, f)
}

func TestVoiceConversationDelegatesCompleteBrowserTask(t *testing.T) {
	f := conversationFixture(t, nil)
	conversationText(t, f)
	instruction := "Find GothamChess beginner videos on YouTube and create a private playlist with five videos."
	args, err := json.Marshal(map[string]any{"instruction": instruction})
	if err != nil {
		t.Fatal(err)
	}
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "function-call-arguments-done", CallID: "browser", Name: "start_task", Arguments: string(args)}
	f.responseDone("completed")
	e := f.read(t)
	// The task decides whether it needs a browser; voice passes the whole request.
	if e["type"] != "tool.call" || e["name"] != "start_task" || e["needsBrowser"] != nil || e["instruction"] != instruction {
		t.Fatal(e)
	}
	// The task must still use the existing durable task-binding check.
	conversationSend(t, f, map[string]string{"type": "tool.result", "callId": "browser", "invocationId": "foreign"})
	if e := f.read(t); e["type"] != "error" {
		t.Fatal(e)
	}
	voiceFinished(t, f)
}

func TestVoiceConversationRejectsRoutingFlags(t *testing.T) {
	// Screens open on demand inside the task, so voice tools carry no routing hints.
	for _, tc := range []struct {
		name, arguments string
	}{
		{"start_task", `{"instruction":"Browse","needs_browser":true}`},
		{"start_task", `{"instruction":"Look","needs_screen":true}`},
		{"get_context", `{"instruction":"Read"}`},
		{"steer_task", `{"instruction":"Continue","needs_browser":true}`},
	} {
		t.Run(tc.name+tc.arguments, func(t *testing.T) {
			if _, err := parseConversationTool(agent.VoiceRealtimeEvent{CallID: "browser", Name: tc.name, Arguments: tc.arguments}); err == nil {
				t.Fatal("accepted unexpected voice tool arguments")
			}
		})
	}
	tool, err := parseConversationTool(agent.VoiceRealtimeEvent{CallID: "ordinary", Name: "start_task", Arguments: `{"instruction":"Organize files"}`})
	if err != nil || tool.Instruction != "Organize files" {
		t.Fatalf("ordinary task changed: %+v %v", tool, err)
	}
}

func TestVoiceConversationReadToolContinuesOnSameSession(t *testing.T) {
	f := conversationFixture(t, nil)
	conversationText(t, f)
	f.provider.events <- agent.VoiceRealtimeEvent{Type: "function-call-arguments-done", CallID: "status", Name: "get_task_status", Arguments: `{}`}
	f.responseDone("completed")
	event := voiceSent(t, f, "conversation-item-create")
	if event["item"].(map[string]any)["callId"] != "status" {
		t.Fatal(event)
	}
	voiceSent(t, f, "response-create")
	conversationComplete(t, f)
}
func TestVoiceConversationRejectsUnknownAndOversizedTools(t *testing.T) {
	for _, e := range []agent.VoiceRealtimeEvent{{CallID: "x", Name: "delete_root", Arguments: `{}`}, {CallID: "x", Name: "start_task", Arguments: `{"instruction":""}`}, {CallID: "x", Name: "cancel_task", Arguments: `{"invocation_id":"foreign"}`}} {
		if _, err := parseConversationTool(e); err == nil {
			t.Fatal(e)
		}
	}
}

func voiceSent(t *testing.T, f *voiceFixture, want string) map[string]any {
	t.Helper()
	select {
	case e := <-f.provider.sent:
		if e["type"] != want {
			t.Fatalf("wanted %s, got %v", want, e)
		}
		return e
	case <-time.After(3 * time.Second):
		t.Fatalf("missing provider event %s", want)
	}
	return nil
}

func voiceFinished(t *testing.T, f *voiceFixture) {
	t.Helper()
	select {
	case <-f.finished:
	case <-time.After(3 * time.Second):
		t.Fatal("session did not finish")
	}
}
