package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
)

const maxAgentVoiceRecordingBytes = 10 << 20
const maxAgentVoiceJSONBytes = (maxAgentVoiceRecordingBytes*4)/3 + (1 << 20)

func (s *AgentsService) AgentVoiceTranscription() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.requireUser(w, r); !ok {
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
		text, language, actualDurationMS, err := s.voiceAnalyzer.TranscribeAgentVoice(r.Context(), audio, mimeType, durationMS)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "voice_transcription_failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"transcript": text, "detected_language": language, "duration_ms": actualDurationMS})
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
