package api

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	agent "github.com/kannachi323/misty/server/internal/agents"
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
		writeJSON(w, http.StatusCreated, map[string]any{"ticket": token, "expires_in": 60, "webrtc": agent.VoiceWebRTCConfigured()})
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
		upgrader := websocket.Upgrader{Subprotocols: []string{"misty-voice-v1"}, CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		operation := "realtime-voice:" + uuid.NewString()
		reservation, err := s.database.BillingService().Reserve(ctx, billingadapter.Request{
			Version: 1, AccountID: user, Operation: "agent.voice.realtime", OperationID: operation, Key: operation,
			Usage: billingadapter.Usage{Provider: "openai", Model: agent.AgentRealtimeModel, Units: agent.RealtimeVoiceEstimate(), Estimated: true},
		})
		if err != nil {
			_ = conn.WriteJSON(map[string]string{"type": "error", "message": "Voice admission failed. Check your usage or retry later."})
			return
		}
		if err = s.database.RecordVoiceUsage(ctx, reservation, "active", map[string]int64{}); err != nil {
			_ = s.releaseAgentVoice(reservation)
			_ = conn.WriteJSON(map[string]string{"type": "error", "message": "Voice accounting is unavailable. Please try again."})
			return
		}
		provider, err := s.openVoiceTransport(ctx, conn, r.URL.Query().Get("transport") == "webrtc")
		if err != nil {
			// Persist the final decision before enqueueing it. A crash or outbox
			// failure can then recover the unused reservation with the same key.
			finish, stop := context.WithTimeout(context.Background(), 20*time.Second)
			defer stop()
			if err := s.database.RecordVoiceUsage(finish, reservation, "settlement_pending", map[string]int64{}); err == nil {
				if err = s.releaseAgentVoice(reservation); err == nil {
					err = s.database.RecordVoiceUsage(finish, reservation, "closed", map[string]int64{})
				}
				if err != nil {
					log.Printf("voice_usage_pending operation_id=%q", operation)
				}
			} else {
				log.Printf("voice_usage_pending operation_id=%q", operation)
			}
			_ = conn.WriteJSON(map[string]string{"type": "error", "message": "The realtime voice provider is unavailable. Please try again."})
			return
		}
		defer provider.Close()
		transport := "websocket"
		if rtc, ok := provider.(interface{ WebRTC() bool }); ok && rtc.WebRTC() {
			transport = "webrtc"
		}
		log.Printf("voice_transport operation_id=%q transport=%s", operation, transport)
		s.runVoiceRealtime(ctx, conn, provider, reservation, user, device)
	}
}
