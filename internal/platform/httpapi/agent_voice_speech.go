package api

import (
	"context"
	"encoding/json"
	"errors"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

var agentSpeechPoint = regexp.MustCompile(`\[POINT:[^\]\r\n]*\]`)

// Speech is derived from an owned, completed invocation. Clients cannot use this
// endpoint to synthesize arbitrary text or make an unfinished action sound done.
func (s *AgentsService) AgentVoiceSpeech() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			InvocationID string `json:"invocation_id"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || strings.TrimSpace(body.InvocationID) == "" || len(body.InvocationID) > 160 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_speech_request"})
			return
		}
		if s.voiceAnalyzer == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "voice_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		settings, _, err := s.database.AISettings(ctx, userID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		if !settings.Enabled {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "ai_disabled"})
			return
		}
		_, err = s.database.AIInvocationByID(ctx, userID, body.InvocationID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		events, state, err := s.database.AIInvocationEvents(ctx, userID, body.InvocationID, 0)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		text, err := agentSpeechText(state, events)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "speech_reply_unavailable"})
			return
		}
		if s.voiceLimiter != nil {
			if allowed, retry := s.voiceLimiter.Allow(userID, time.Now()); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
				w.Header().Set("X-Misty-RateLimit-Scope", "route")
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "speech_rate_limited"})
				return
			}
		}
		reservation, err := s.reserveAgentVoice(userID, serveragent.AgentVoiceUsage{Model: serveragent.AgentSpeechModel, DurationMS: max(int64(1000), int64(utf8.RuneCountInString(text))*100)})
		if err != nil {
			writeAgentError(w, err)
			return
		}
		started := time.Now()
		audio, mimeType, usage, err := s.voiceAnalyzer.GenerateAgentSpeechWithUsage(ctx, text, "alloy")
		outcome := "completed"
		if err != nil {
			outcome = "failed"
			_ = s.releaseAgentVoice(reservation)
		}
		s.voiceMetrics.RecordAIInvocation("agents", "speech", serveragent.AgentSpeechModel, outcome, time.Since(started), 0)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "speech_generation_failed"})
			return
		}
		if err := s.settleAgentVoice(reservation, usage); err != nil {
			// Keep the reservation: provider work has already been consumed.
			writeAgentError(w, err)
			return
		}
		w.Header().Set("Content-Type", mimeType)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(audio)
	}
}

func agentSpeechText(state string, events []db.AIInvocationEventRecord) (string, error) {
	if state != "completed" {
		return "", errors.New("reply is not complete")
	}
	text := ""
	for _, event := range events {
		if event.EventType != "assistant.message" {
			continue
		}
		var message struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(event.Payload, &message) != nil {
			return "", errors.New("invalid reply")
		}
		text = message.Text
	}
	text = strings.TrimSpace(agentSpeechPoint.ReplaceAllString(text, ""))
	if text == "" {
		return "", errors.New("empty reply")
	}
	// Spoken playback is bounded; the complete answer remains in the conversation.
	runes := []rune(text)
	if len(runes) > 5800 {
		text = string(runes[:5800]) + ". The rest of my answer is in the conversation."
	}
	return text, nil
}
