package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRealtimeGatewayUsesServerSecretAndManualTurnProtocol(t *testing.T) {
	t.Setenv("MISTY_AGENT_MODEL_PROVIDER", "gateway")
	t.Setenv("MISTY_AGENT_MODEL", "")
	t.Setenv("MISTY_AGENT_MODEL_API_KEY", "")
	t.Setenv("MISTY_AGENT_MODEL_BASE_URL", "")
	configured := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/realtime/client-secrets" {
			if r.Header.Get("Authorization") != "Bearer fixture-key" {
				t.Error("missing server credential")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["model"] != AgentRealtimeModel {
				t.Error("wrong model")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "fixture-short-lived"})
			return
		}
		if r.URL.Path != "/v4/ai/realtime-model" || r.URL.Query().Get("ai-model-id") != AgentRealtimeModel {
			t.Error("wrong websocket route")
		}
		protocols := websocket.Subprotocols(r)
		if len(protocols) != 2 || protocols[1] != "ai-gateway-auth.fixture-short-lived" {
			t.Error("wrong socket authentication")
		}
		upgrader := websocket.Upgrader{Subprotocols: []string{"ai-gateway-realtime.v1"}}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var event map[string]any
		if conn.ReadJSON(&event) == nil {
			configured <- event
		}
	}))
	defer server.Close()
	t.Setenv("AI_GATEWAY_EMBEDDING_BASE_URL", server.URL+"/v4/ai")
	a := &SmartLibraryAnalyzer{APIKey: "fixture-key", Client: server.Client()}
	session, err := a.OpenVoiceRealtime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err = session.Configure(); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	select {
	case event = <-configured:
	case <-time.After(5 * time.Second):
		t.Fatal("gateway configuration did not arrive")
	}
	config := event["config"].(map[string]any)
	if config["turnDetection"].(map[string]any)["type"] != "disabled" {
		t.Fatal("automatic turn control enabled")
	}
	if _, exists := config["tools"]; exists {
		t.Fatal("voice must not have desktop tools")
	}
}

func TestRealtimeUsageRequiresCompleteModalities(t *testing.T) {
	valid := json.RawMessage(`{"response":{"usage":{"input_tokens":15,"output_tokens":10,"input_token_details":{"text_tokens":10,"audio_tokens":5,"cached_tokens":3,"cached_tokens_details":{"text_tokens":1,"audio_tokens":2}},"output_token_details":{"text_tokens":4,"audio_tokens":6}}}}`)
	u, err := RealtimeResponseUsage(valid)
	if err != nil || u["cached_input_audio_tokens"] != 2 || u["output_audio_tokens"] != 6 {
		t.Fatal(u, err)
	}
	for _, raw := range []string{`{}`, `{"response":{"usage":{}}}`, `{"response":{"usage":{"input_tokens":2,"input_token_details":{"text_tokens":1},"output_token_details":{}}}}`, `{"response":{"usage":{"input_tokens":0,"output_tokens":0,"input_token_details":{},"output_token_details":{}}}}`} {
		if _, err := RealtimeResponseUsage(json.RawMessage(raw)); err == nil {
			t.Fatal("accepted ambiguous usage", raw)
		}
	}
	u, err = RealtimeTranscriptionUsage(json.RawMessage(`{"usage":{"type":"tokens","input_tokens":15,"output_tokens":10}}`))
	if err != nil || u["transcription_input_tokens"] != 15 {
		t.Fatal(u, err)
	}
	if _, err = RealtimeTranscriptionUsage(json.RawMessage(`{"usage":{"type":"duration","seconds":2}}`)); err == nil {
		t.Fatal("duration misreported as zero tokens")
	}
	if VoiceRealtimeManualSession(json.RawMessage(`{"session":{"audio":{"input":{}}}}`)) {
		t.Fatal("missing VAD accepted")
	}
	if !VoiceRealtimeManualSession(json.RawMessage(`{"session":{"audio":{"input":{"turn_detection":null}}}}`)) {
		t.Fatal("manual VAD rejected")
	}
}
