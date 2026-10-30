package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/byok"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

const liveTicketTTL = time.Minute

// VoiceLiveConfig is live transcription through the agent runtime's relay.
// It is off unless MISTY_VOICE_LIVE=true and both the runtime and the relay's
// public URL are configured.
type VoiceLiveConfig struct {
	Enabled bool
	// URL is the relay's public WebSocket address, for example
	// wss://runtime.example.com/v1/live/transcribe.
	URL           string
	TicketMinutes int64
	runtime       AgentRuntimeConfig
}

func VoiceLiveConfigFromEnv(runtime AgentRuntimeConfig) VoiceLiveConfig {
	minutes := int64(15)
	if parsed, err := strconv.ParseInt(strings.TrimSpace(envconfig.Getenv("MISTY_VOICE_LIVE_TICKET_MINUTES")), 10, 64); err == nil && parsed > 0 && parsed <= 240 {
		minutes = parsed
	}
	url := strings.TrimSpace(envconfig.Getenv("MISTY_VOICE_LIVE_URL"))
	return VoiceLiveConfig{
		Enabled:       strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_VOICE_LIVE")), "true") && runtime.Enabled() && (strings.HasPrefix(url, "wss://") || strings.HasPrefix(url, "ws://localhost") || strings.HasPrefix(url, "ws://127.0.0.1")),
		URL:           url,
		TicketMinutes: minutes,
		runtime:       runtime,
	}
}

func (s *AgentsService) SetVoiceLive(config VoiceLiveConfig) { s.voiceLive = config }

// VoiceLiveTicket issues a short-lived signed ticket for one live session.
// The microphone is opened by the person, never by an
// agent; this only admits the stream.
func (s *AgentsService) VoiceLiveTicket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if !s.voiceLive.Enabled {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "voice_live_unavailable", "message": "Live transcription isn't available yet."})
			return
		}
		var body struct {
			DeviceID string   `json:"device_id"`
			Channels []string `json:"channels"`
			Language string   `json:"language"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		if decodeAIJSON(w, r, &body) != nil || !deviceIDPattern.MatchString(body.DeviceID) || len(body.Language) > 16 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_voice_request"})
			return
		}
		channels, valid := liveChannels(body.Channels)
		if !valid {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_voice_channels"})
			return
		}
		if err := s.liveVoiceAccess(r.Context(), user, body.DeviceID); err != nil {
			writeAgentError(w, err)
			return
		}
		if s.voiceLimiter != nil {
			if allowed, _ := s.voiceLimiter.Allow(user, time.Now()); !allowed {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "voice_rate_limited", "message": "Too many voice sessions in a minute. Try again shortly."})
				return
			}
		}
		maxMS := s.voiceLive.TicketMinutes * 60_000 * int64(len(channels))
		ticket, expires, err := s.issueLiveTicket(r.Context(), user, body.DeviceID, channels, body.Language, maxMS)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, map[string]any{"ticket": ticket, "url": s.voiceLive.URL, "expires_in": int(liveTicketTTL.Seconds()), "max_ms": maxMS, "expires_at": expires.UTC().Format(time.RFC3339)})
	}
}

func (s *AgentsService) liveVoiceAccess(ctx context.Context, user, device string) error {
	settings, _, err := s.database.AISettings(ctx, user)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return errAIDisabled
	}
	_, err = s.database.TrustedDevicePublicKey(user, device)
	return err
}

// liveTranscriptionModel is the Soniox model a live session streams to: the
// account's transcription model when it is a Soniox one, else the default.
// The account needs its own Soniox key unless the operator has models.
func liveTranscriptionModel(ctx context.Context, user string) (string, error) {
	model := defaultRoleModel("live-transcription")
	c := accountCredentials(ctx, user)
	if c != nil {
		if chosen := c.RoleModel("live-transcription"); byok.ProviderOf(chosen) == "soniox" {
			model = chosen
		}
	}
	if _, ok := c.CredentialFor(model); !ok && !aimodels.OperatorModels() {
		return "", byok.ErrModelKeyNeeded
	}
	return model, nil
}

func (s *AgentsService) issueLiveTicket(ctx context.Context, user, device string, channels []string, language string, maxMS int64) (string, time.Time, error) {
	session := uuid.NewString()
	model, err := liveTranscriptionModel(ctx, user)
	if err != nil {
		return "", time.Time{}, err
	}
	now := time.Now()
	// The session may stream for its whole length after connecting, so it
	// stays open until then plus a margin.
	if err := s.database.CreateVoiceLiveSession(ctx, db.VoiceLiveSession{ID: session, User: user, Device: device, Channels: channels, MaxMS: maxMS,
		ExpiresAt: now.Add(liveTicketTTL + time.Duration(maxMS)*time.Millisecond + 10*time.Minute)}); err != nil {
		return "", time.Time{}, err
	}
	expires := now.Add(liveTicketTTL)
	ticket, err := signLiveTicket(s.voiceLive.runtime.secret, LiveTicketClaims{SessionID: session, User: user, Device: device, Channels: channels, MaxMS: maxMS, Language: language, Model: model, ExpiresAt: expires.Unix()})
	return ticket, expires, err
}
