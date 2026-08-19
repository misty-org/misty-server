package api

import (
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
)

const maxAgentVoiceRecordingBytes = 10 << 20

func (s *AgentsService) AgentVoiceTranscription() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.requireUser(w, r); !ok {
			return
		}
		if s.voiceAnalyzer == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "voice_unavailable"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxAgentVoiceRecordingBytes+(1<<20))
		if err := r.ParseMultipartForm(maxAgentVoiceRecordingBytes); err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "voice_recording_too_large"})
			return
		}
		file, header, err := r.FormFile("audio")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "voice_recording_required"})
			return
		}
		defer file.Close()
		durationMS, _ := strconv.ParseInt(r.FormValue("duration_ms"), 10, 64)
		if durationMS <= 0 || durationMS > 60_000 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "voice_duration_invalid"})
			return
		}
		audio, err := io.ReadAll(io.LimitReader(file, maxAgentVoiceRecordingBytes+1))
		if err != nil || len(audio) == 0 || len(audio) > maxAgentVoiceRecordingBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "voice_recording_too_large"})
			return
		}
		mimeType := agentVoiceMIMEType(header)
		text, language, actualDurationMS, err := s.voiceAnalyzer.TranscribeAgentVoice(r.Context(), audio, mimeType, durationMS)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "voice_transcription_failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"transcript": text, "detected_language": language, "duration_ms": actualDurationMS})
	}
}

func (s *AgentsService) AgentVoiceSpeech() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if s.voiceAnalyzer == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "voice_unavailable"})
			return
		}
		var body struct {
			AgentID string `json:"agent_id"`
			Text    string `json:"response_text"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		agent, err := s.database.PersonalAgentByID(r.Context(), userID, strings.TrimSpace(body.AgentID))
		if err != nil {
			writeAgentError(w, err)
			return
		}
		audio, contentType, err := s.voiceAnalyzer.GenerateAgentSpeech(r.Context(), body.Text, agent.VoiceID)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "voice_speech_failed"})
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(audio)
	}
}

func agentVoiceMIMEType(header *multipart.FileHeader) string {
	value := strings.TrimSpace(header.Header.Get("Content-Type"))
	if value == "" {
		return "audio/webm"
	}
	return value
}
