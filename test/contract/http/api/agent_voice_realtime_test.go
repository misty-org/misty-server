package api

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	agent "github.com/kannachi323/misty/server/internal/agents"
)


import ()

func serveVoiceGatewayFixture(t *testing.T, w http.ResponseWriter, r *http.Request, outputs *atomic.Int32) {
	t.Helper()
	if r.URL.Path == "/v1/realtime/client-secrets" {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "fixture"})
		return
	}
	upgrader := websocket.Upgrader{Subprotocols: []string{"ai-gateway-realtime.v1"}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		var event map[string]json.RawMessage
		if conn.ReadJSON(&event) != nil {
			return
		}
		var kind string
		_ = json.Unmarshal(event["type"], &kind)
		switch kind {
		case "session-update":
			var config struct {
				ProviderOptions struct {
					Limit int `json:"max_output_tokens"`
				} `json:"providerOptions"`
			}
			if err := json.Unmarshal(event["config"], &config); err != nil {
				t.Error(err)
				return
			}
			raw, _ := json.Marshal(map[string]any{"session": map[string]any{"max_output_tokens": config.ProviderOptions.Limit, "audio": map[string]any{"input": map[string]any{"turn_detection": nil}}}})
			_ = conn.WriteJSON(agent.VoiceRealtimeEvent{Type: "session-updated", Raw: raw})
		case "conversation-item-create":
			if !strings.Contains(string(event["item"]), "Blue lantern.") || strings.Contains(string(event["item"]), "POINT") {
				t.Error("unexpected canonical reply")
			}
		case "response-create":
			outputs.Add(1)
			_ = conn.WriteJSON(agent.VoiceRealtimeEvent{Type: "response-created", ResponseID: "fixture-output"})
			_ = conn.WriteJSON(agent.VoiceRealtimeEvent{Type: "audio-delta", ResponseID: "fixture-output", Delta: "AAAAAA=="})
			_ = conn.WriteJSON(agent.VoiceRealtimeEvent{Type: "response-done", ResponseID: "fixture-output", Status: "completed", Raw: json.RawMessage(`{"response":{"usage":{"input_tokens":10,"output_tokens":2,"input_token_details":{"text_tokens":10,"audio_tokens":0,"cached_tokens":0},"output_token_details":{"text_tokens":1,"audio_tokens":1}}}}`)})
			return
		}
	}
}
