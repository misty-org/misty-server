package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	agent "github.com/kannachi323/misty/server/internal/agents"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in synthetic speech through the actual server actor and Gateway. It
// reports latency and buffer starvation, never prompts, keys or audio.
func TestVoiceConversationLivePCM(t *testing.T) {
	path := os.Getenv("MISTY_REALTIME_CONVERSATION_PCM")
	if path == "" {
		t.Skip("requires explicit synthetic PCM and configured gateway")
	}
	pcm, err := os.ReadFile(path)
	if err != nil || len(pcm) < 7200 || len(pcm) > 240000 {
		t.Fatal("invalid synthetic PCM")
	}
	a := &agent.SmartLibraryAnalyzer{APIKey: os.Getenv("AI_GATEWAY_API_KEY")}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	provider, err := a.OpenVoiceRealtime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	reserved := agent.RealtimeVoiceUsage{}
	measured := agent.RealtimeVoiceUsage{}
	finished := make(chan struct{})
	hooks := conversationHooks{Operation: "realtime-voice:synthetic-live", History: "[]", Access: func(context.Context) error { return nil }, Input: func(context.Context, int) error { return nil }, Reserve: func(_ context.Context, u agent.RealtimeVoiceUsage) error { reserved.Add(u); return nil }, Settle: func(_ context.Context, u agent.RealtimeVoiceUsage) error { measured = u; return nil }, Checkpoint: func(context.Context, string, agent.RealtimeVoiceUsage) error { return nil }, Save: func(context.Context, string, string, string, bool, time.Time) error { return nil }, Tool: func(_ context.Context, name string) (string, error) {
		if name == "get_context" {
			return "[]", nil
		}
		return `{"state":"none"}`, nil
	}, TaskID: func() string { return "" }, Bind: func(context.Context, string, string, string) (string, error) { return `{"state":"none"}`, nil }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		defer close(finished)
		runConversationSession(ctx, c, provider, hooks)
	}))
	defer server.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	read := func() map[string]any {
		t.Helper()
		_ = client.SetReadDeadline(time.Now().Add(35 * time.Second))
		var e map[string]any
		if err := client.ReadJSON(&e); err != nil {
			t.Fatal(err)
		}
		if e["type"] == "error" {
			t.Fatal(e)
		}
		return e
	}
	if e := read(); e["type"] != "ready" {
		t.Fatal(e)
	}
	latencies := []int64{}
	totalUnderruns := 0
	for turn := 0; turn < 2; turn++ {
		client.WriteJSON(map[string]string{"type": "turn.begin"})
		if e := read(); e["type"] != "turn.ready" {
			t.Fatal(e)
		}
		for offset, sequence := 0, 0; offset < len(pcm); sequence++ {
			end := offset + 9600
			if end > len(pcm) {
				end = len(pcm)
			}
			client.WriteJSON(map[string]any{"type": "audio.append", "sequence": sequence, "audio": base64.StdEncoding.EncodeToString(pcm[offset:end])})
			offset = end
		}
		start := time.Now()
		client.WriteJSON(map[string]string{"type": "audio.commit"})
		audioBytes := 0
		var playbackEnd time.Time
		transcribed := false
		for {
			e := read()
			switch e["type"] {
			case "transcript":
				transcribed = strings.TrimSpace(e["text"].(string)) != ""
			case "audio":
				b, err := base64.StdEncoding.DecodeString(e["audio"].(string))
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now()
				if audioBytes == 0 {
					latencies = append(latencies, time.Since(start).Milliseconds())
					playbackEnd = now.Add(250 * time.Millisecond)
				} else if now.After(playbackEnd) {
					totalUnderruns++
					playbackEnd = now.Add(375 * time.Millisecond)
				}
				playbackEnd = playbackEnd.Add(time.Duration(len(b)) * time.Second / 48000)
				audioBytes += len(b)
			case "audio.done":
				client.WriteJSON(map[string]string{"type": "playback.done"})
			case "turn.done":
				if !transcribed || audioBytes == 0 || e["reply"] == "" {
					t.Fatal("missing voice turn")
				}
				goto next
			}
		}
	next:
	}
	client.Close()
	<-finished
	for k, n := range measured {
		if strings.HasPrefix(k, "input_") || strings.HasPrefix(k, "output_") {
			if n > reserved[k] {
				t.Fatalf("unreserved %s: %d > %d", k, n, reserved[k])
			}
		}
	}
	report := map[string]any{"scope": "real Gateway + server actor; synthetic PCM; no native microphone/speakers", "first_audio_ms": latencies, "projected_250ms_buffer_underruns": totalUnderruns, "persistent_turns": 2, "measured_usage_within_reservation": true}
	raw, _ := json.Marshal(report)
	t.Log(string(raw))
	if out := os.Getenv("MISTY_REALTIME_CONVERSATION_REPORT"); out != "" {
		if err = os.WriteFile(out, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// Opt-in: the real realtime model, through the server actor, routes a
// question about the screen to show_on_screen, work to start_task, and small
// talk to no tool at all. Typed turns stand in for speech.
func TestVoiceConversationLiveRoutesScreenQuestions(t *testing.T) {
	if os.Getenv("MISTY_REALTIME_CONVERSATION_LIVE") != "1" {
		t.Skip("requires MISTY_REALTIME_CONVERSATION_LIVE=1 and a configured gateway")
	}
	a := &agent.SmartLibraryAnalyzer{APIKey: os.Getenv("AI_GATEWAY_API_KEY")}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	provider, err := a.OpenVoiceRealtime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	hooks := conversationHooks{Operation: "realtime-voice:routing-live", History: "[]", Access: func(context.Context) error { return nil }, Input: func(context.Context, int) error { return nil }, Reserve: func(context.Context, agent.RealtimeVoiceUsage) error { return nil }, Settle: func(context.Context, agent.RealtimeVoiceUsage) error { return nil }, Checkpoint: func(context.Context, string, agent.RealtimeVoiceUsage) error { return nil }, Save: func(context.Context, string, string, string, bool, time.Time) error { return nil }, Tool: func(_ context.Context, name string) (string, error) {
		if name == "get_context" {
			return "[]", nil
		}
		return `{"state":"none"}`, nil
	}, TaskID: func() string { return "" }, Bind: func(context.Context, string, string, string) (string, error) {
		return `{"state":"completed","note":"live routing check; nothing was admitted"}`, nil
	}}
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		defer close(finished)
		runConversationSession(ctx, c, provider, hooks)
	}))
	defer server.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	read := func() map[string]any {
		t.Helper()
		_ = client.SetReadDeadline(time.Now().Add(45 * time.Second))
		var e map[string]any
		if err := client.ReadJSON(&e); err != nil {
			t.Fatal(err)
		}
		if e["type"] == "error" {
			t.Fatal(e)
		}
		return e
	}
	if e := read(); e["type"] != "ready" {
		t.Fatal(e)
	}
	routes := map[string]string{} // prompt → tool, and how much it said around the hand-off
	for _, c := range []struct{ prompt, want string }{
		{"where's the push button on my screen?", "start_task"},
		{"walk me through committing my changes in the app I have open", "start_task"},
		{"please push my branch to github for me", "start_task"},
		{"thanks, how's your day going?", ""},
	} {
		_ = client.WriteJSON(map[string]string{"type": "turn.begin"})
		if e := read(); e["type"] != "turn.ready" {
			t.Fatal(e)
		}
		_ = client.WriteJSON(map[string]string{"type": "text.commit", "text": c.prompt})
		called, spoken, said := "", 0, ""
		for done := false; !done; {
			e := read()
			switch e["type"] {
			case "audio":
				spoken += len(e["audio"].(string))
			case "tool.call":
				if called == "" {
					called = e["name"].(string)
				}
				_ = client.WriteJSON(map[string]string{"type": "tool.result", "callId": e["callId"].(string), "invocationId": "live-check"})
			case "audio.done":
				_ = client.WriteJSON(map[string]string{"type": "playback.done"})
			case "turn.done":
				done = true
				said, _ = e["reply"].(string)
			}
		}
		routes[c.prompt] = fmt.Sprintf("%s (spoken audio bytes: %d) said: %q", called, spoken, said)
		if called != c.want {
			t.Errorf("%q called %q, want %q", c.prompt, called, c.want)
		}
		if called != "" && (spoken > 0 || strings.TrimSpace(said) != "") {
			t.Errorf("%q announced its hand-off: %q", c.prompt, said)
		}
	}
	raw, _ := json.Marshal(routes)
	t.Log(string(raw))
	client.Close()
	<-finished
}
