package api

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

const voiceTicketPrefix = "misty-voice-auth."

// These short-lived, single-use tickets are domain-separated from sync tickets
// and bind an authenticated account to one of its non-revoked devices.
func (s *AgentsService) AgentVoiceRealtimeTicket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			DeviceID string `json:"device_id"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		if decodeAIJSON(w, r, &body) != nil || !deviceIDPattern.MatchString(body.DeviceID) {
			writeJSON(w, 400, map[string]string{"code": "invalid_voice_device"})
			return
		}
		if err := s.voiceAccess(r.Context(), user, body.DeviceID); err != nil {
			writeAgentError(w, err)
			return
		}
		if s.voiceLimiter != nil {
			if allowed, _ := s.voiceLimiter.Allow(user, time.Now()); !allowed {
				writeJSON(w, 429, map[string]string{"code": "voice_rate_limited"})
				return
			}
		}
		token, _, err := randomToken()
		if err != nil {
			writeAgentError(w, err)
			return
		}
		token = body.DeviceID + "." + token
		if err = s.database.CreateRealtimeTicket(r.Context(), user, voiceTicketHash(token), 0, time.Now().Add(time.Minute)); err != nil {
			writeAgentError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, map[string]any{"ticket": token, "expires_in": 60 })
	}
}

func voiceTicketHash(token string) string { return security.HashToken("companion-realtime:" + token) }

func (s *AgentsService) voiceAccess(ctx context.Context, user, device string) error {
	if s.voiceAnalyzer == nil {
		return billingadapter.ErrUnavailable
	}
	settings, _, err := s.database.AISettings(ctx, user)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return billingadapter.ErrDenied
	}
	_, err = s.database.TrustedDevicePublicKey(user, device)
	return err
}

func (s *AgentsService) AgentVoiceRealtimeConnect() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			writeJSON(w, 426, map[string]string{"code": "websocket_upgrade_required"})
			return
		}
		token := ""
		for _, protocol := range websocket.Subprotocols(r) {
			if strings.HasPrefix(protocol, voiceTicketPrefix) {
				token = strings.TrimPrefix(protocol, voiceTicketPrefix)
			}
		}
		device, _, found := strings.Cut(token, ".")
		if !found || len(token) > 256 || !deviceIDPattern.MatchString(device) {
			writeJSON(w, 401, map[string]string{"code": "invalid_ticket"})
			return
		}
		user, _, err := s.database.ConsumeRealtimeTicket(r.Context(), voiceTicketHash(token))
		if err != nil {
			writeJSON(w, 401, map[string]string{"code": "invalid_ticket"})
			return
		}
		if err = s.voiceAccess(r.Context(), user, device); err != nil {
			writeAgentError(w, err)
			return
		}
		// Voice is always a conversation; the single-turn relay was retired.
		conversation := r.URL.Query().Get("conversation")
		if r.URL.Query().Get("mode") != "conversation" || conversation == "" || len(conversation) > 160 {
			writeJSON(w, 400, map[string]string{"code": "invalid_voice_conversation"})
			return
		}
		history, err := s.voiceConversationContext(r.Context(), user, conversation)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		upgrader := websocket.Upgrader{Subprotocols: []string{"misty-voice-v1"}, CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		operation := "realtime-voice:" + uuid.NewString()
		setupStarted := time.Now()
		provider, err := s.openVoiceTransport(ctx, user)
		if err != nil {
			rejectVoiceSetup(conn, websocket.CloseInternalServerErr, "The realtime voice provider is unavailable. Please try again.")
			return
		}
		defer provider.Close()
		log.Printf("voice_transport operation_id=%q setup_ms=%d", operation, time.Since(setupStarted).Milliseconds())
		s.runVoiceConversation(ctx, conn, provider, operation, user, device, conversation, history)
	}
}

// Only used before runVoiceSession starts its reader. Finish the close handshake
// so a setup rejection reaches the client instead of looking like network loss.
// Drain any in-flight offer/input while waiting, with bounded time and size.
func rejectVoiceSetup(conn *websocket.Conn, code int, message string) {
	deadline := time.Now().Add(time.Second)
	_ = conn.SetWriteDeadline(deadline)
	if conn.WriteJSON(map[string]string{"type": "error", "message": message}) != nil {
		return
	}
	if conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, "Voice setup failed."), deadline) != nil {
		return
	}
	conn.SetReadLimit(100 << 10)
	_ = conn.SetReadDeadline(deadline)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
