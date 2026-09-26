package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxAgentVoiceRecordingBytes = 10 << 20
const maxAgentVoiceJSONBytes = (maxAgentVoiceRecordingBytes*4)/3 + (1 << 20)

func (s *AgentsService) AgentVoiceTranscription() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if s.voiceAnalyzer == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "voice_unavailable"})
			return
		}
		audio, mimeType, durationMS, code := readAgentVoiceRecording(w, r)
		if code != "" {
			status := http.StatusBadRequest
			if code == "voice_recording_too_large" {
				status = http.StatusRequestEntityTooLarge
			}
			writeJSON(w, status, map[string]string{"code": code})
			return
		}
		if durationMS <= 0 || durationMS > 60_000 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "voice_duration_invalid"})
			return
		}
		if len(audio) == 0 || len(audio) > maxAgentVoiceRecordingBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "voice_recording_too_large"})
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
		if s.voiceLimiter != nil {
			if allowed, retry := s.voiceLimiter.Allow(userID, time.Now()); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "voice_rate_limited"})
				return
			}
		}
		started := time.Now()
		text, language, usage, err := s.voiceAnalyzer.TranscribeAgentVoiceWithBilling(ctx, audio, mimeType, durationMS, s.database.BillingService(), userID)
		outcome := "completed"
		if err != nil {
			outcome = "failed"
		}
		s.voiceMetrics.RecordAIInvocation("agents", "transcription", usage.Model, outcome, time.Since(started), 0)
		if err != nil {
			if errors.Is(err, billingadapter.ErrDenied) || errors.Is(err, billingadapter.ErrUnavailable) || errors.Is(err, billingadapter.ErrConflict) {
				writeBillingError(w, err)
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "voice_transcription_failed"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"transcript": text, "detected_language": language, "duration_ms": usage.DurationMS})
	}
}

func readAgentVoiceRecording(w http.ResponseWriter, r *http.Request) ([]byte, string, int64, string) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "application/json") {
		r.Body = http.MaxBytesReader(w, r.Body, maxAgentVoiceJSONBytes)
		var body struct {
			AudioBase64 string `json:"audio_base64"`
			MIMEType    string `json:"mime_type"`
			DurationMS  int64  `json:"duration_ms"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				return nil, "", 0, "voice_recording_too_large"
			}
			return nil, "", 0, "voice_recording_required"
		}
		if decoder.Decode(new(any)) != io.EOF {
			return nil, "", 0, "voice_recording_required"
		}
		audio, err := base64.StdEncoding.DecodeString(body.AudioBase64)
		if err != nil || len(audio) == 0 {
			return nil, "", 0, "voice_recording_required"
		}
		mimeType := strings.TrimSpace(body.MIMEType)
		if mimeType == "" {
			mimeType = "audio/webm"
		}
		return audio, mimeType, body.DurationMS, ""
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAgentVoiceRecordingBytes+(1<<20))
	if err := r.ParseMultipartForm(maxAgentVoiceRecordingBytes); err != nil {
		return nil, "", 0, "voice_recording_too_large"
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("audio")
	if err != nil {
		return nil, "", 0, "voice_recording_required"
	}
	defer file.Close()
	durationMS, _ := strconv.ParseInt(r.FormValue("duration_ms"), 10, 64)
	audio, err := io.ReadAll(io.LimitReader(file, maxAgentVoiceRecordingBytes+1))
	if err != nil || len(audio) == 0 || len(audio) > maxAgentVoiceRecordingBytes {
		return nil, "", 0, "voice_recording_too_large"
	}
	return audio, agentVoiceMIMEType(header), durationMS, ""
}

func agentVoiceMIMEType(header *multipart.FileHeader) string {
	value := strings.TrimSpace(header.Header.Get("Content-Type"))
	if value == "" {
		return "audio/webm"
	}
	return value
}
