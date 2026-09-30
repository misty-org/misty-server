package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	agent "github.com/kannachi323/misty/server/internal/agents"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// Exercise real HTTP authentication, database ownership and one-use WS tickets.
// The provider is a fixture; authentication, ownership and journaling are real.
func TestVoiceRealtimeTicketAccountDeviceAndReplay(t *testing.T) {
	database := openPresenceTestDatabase(t)
	owner, err := database.CreateUser("Voice owner", uniqueTestEmail("voice-owner"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Voice other", uniqueTestEmail("voice-other"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{owner.ID, other.ID} {
		if _, err = database.UpdateAISettings(t.Context(), id, true, 30, true, false); err != nil {
			t.Fatal(err)
		}
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	device, err := database.RegisterTrustedDevice(owner.ID, "Voice fixture", base64.RawURLEncoding.EncodeToString(public), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var providerCalls atomic.Int32
	var providerReady atomic.Bool
	var outputRequests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		if !providerReady.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		serveVoiceGatewayFixture(t, w, r, &outputRequests)
	}))
	defer provider.Close()
	t.Setenv("MISTY_AGENT_MODEL_PROVIDER", "gateway")
	t.Setenv("MISTY_AGENT_MODEL", "")
	t.Setenv("MISTY_AGENT_MODEL_API_KEY", "")
	t.Setenv("MISTY_AGENT_MODEL_BASE_URL", "")
	t.Setenv("AI_GATEWAY_EMBEDDING_BASE_URL", provider.URL+"/v4/ai")
	service := NewAgentsService(database)
	service.SetVoiceAnalyzer(&agent.SmartLibraryAnalyzer{APIKey: "fixture-key", Client: provider.Client()})
	router := chi.NewRouter()
	router.Post("/agent-voice/realtime/ticket", service.AgentVoiceRealtimeTicket())
	router.Get("/agent-voice/realtime", service.AgentVoiceRealtimeConnect())
	ownerToken := newConversationTestBearerToken(t, database, owner.ID)
	otherToken := newConversationTestBearerToken(t, database, other.ID)
	body := map[string]string{"device_id": device.ID}
	for _, token := range []string{"", otherToken} {
		r := performConversationRequest(t, router, "POST", "/agent-voice/realtime/ticket", token, body)
		if r.Code < 400 {
			t.Fatalf("unauthorized ticket status %d", r.Code)
		}
	}
	ticket := func() string {
		t.Helper()
		r := performConversationRequest(t, router, "POST", "/agent-voice/realtime/ticket", ownerToken, body)
		if r.Code != 201 || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("ticket status %d", r.Code)
		}
		var result struct {
			Ticket string `json:"ticket"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil || result.Ticket == "" {
			t.Fatal("invalid ticket")
		}
		return result.Ticket
	}
	issued := ticket()
	// A voice ticket cannot authenticate the unrelated synchronization stream.
	if _, _, err := database.ConsumeRealtimeTicket(t.Context(), security.HashToken(issued)); err == nil {
		t.Fatal("ticket crossed authentication domains")
	}
	server := httptest.NewServer(router)
	defer server.Close()
	dial := func(token string) (*websocket.Conn, *http.Response, error) {
		d := websocket.Dialer{HandshakeTimeout: 5 * time.Second, Subprotocols: []string{"misty-voice-v1", "misty-voice-auth." + token}}
		return d.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/agent-voice/realtime", nil)
	}
	conn, _, err := dial(issued)
	if err != nil {
		t.Fatal(err)
	}
	if conn.Subprotocol() != "misty-voice-v1" {
		t.Fatal("secret echoed in negotiated protocol")
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var event map[string]string
	if err := conn.ReadJSON(&event); err != nil || event["type"] != "error" {
		t.Fatal("expected safe provider error", err)
	}
	conn.Close()
	if providerCalls.Load() != 1 {
		t.Fatal("expected one provider admission")
	}
	conn, response, err := dial(issued)
	if conn != nil {
		conn.Close()
	}
	if err == nil || response == nil || response.StatusCode != 401 {
		t.Fatal("ticket replay accepted")
	}
	response.Body.Close()
	providerReady.Store(true)
	for _, scenario := range []string{"foreign", "incomplete", "completed"} {
		userID, state := owner.ID, "completed"
		if scenario == "foreign" {
			userID = other.ID
		}
		if scenario == "incomplete" {
			state = "running"
		}
		id := "invocation_" + uuid.NewString()
		_, _, err := database.CreateAIInvocationRecord(t.Context(), db.AIInvocationRecord{ID: id, UserID: userID, Mode: "quick", SurfaceID: "settings", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{"prompt":"fixture"}`), ExpiresAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.CommitAIInvocationEvent(t.Context(), userID, id, "fixture-answer", "assistant.message", json.RawMessage(`{"text":"Blue lantern. [POINT:1:10:10]"}`), state); err != nil {
			t.Fatal(err)
		}
		client, _, err := dial(ticket())
		if err != nil {
			t.Fatal(err)
		}
		_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
		if err := client.ReadJSON(&event); err != nil || event["type"] != "ready" {
			t.Fatal("voice not ready", err)
		}
		if err := client.WriteJSON(map[string]string{"type": "reply", "invocation_id": id}); err != nil {
			t.Fatal(err)
		}
		if err := client.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		if scenario != "completed" {
			if event["type"] != "error" || outputRequests.Load() != 0 {
				t.Fatal("unowned or incomplete reply reached output")
			}
		} else {
			if event["type"] != "audio" {
				t.Fatal("completed reply missing audio")
			}
			if err := client.ReadJSON(&event); err != nil || event["type"] != "done" {
				t.Fatal("completed reply missing terminal event", err)
			}
		}
		// Observe server closure after its deferred accounting, then inspect DB.
		_, _, _ = client.ReadMessage()
		client.Close()
	}
	if outputRequests.Load() != 1 {
		t.Fatal("expected exactly one authorized output")
	}
	beforeRevocation := providerCalls.Load()
	issued = ticket()
	if err := database.RevokeTrustedDevice(owner.ID, device.ID); err != nil {
		t.Fatal(err)
	}
	conn, response, err = dial(issued)
	if conn != nil {
		conn.Close()
	}
	if err == nil || response == nil || response.StatusCode < 400 {
		t.Fatal("revoked device accepted")
	}
	response.Body.Close()
	r := performConversationRequest(t, router, "POST", "/agent-voice/realtime/ticket", ownerToken, body)
	if r.Code < 400 || providerCalls.Load() != beforeRevocation {
		t.Fatal("revoked device reached provider")
	}
	var pending int
	if err := database.Conn.QueryRowContext(t.Context(), `SELECT count(*) FROM voice_usage_journal WHERE account_id=$1 AND state<>'closed'`, owner.ID).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("voice reservations not finalized", pending, err)
	}
}

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
			_ = conn.WriteJSON(agent.VoiceRealtimeEvent{Type: "session-updated", Raw: json.RawMessage(`{"session":{"audio":{"input":{"turn_detection":null}}}}`)})
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
