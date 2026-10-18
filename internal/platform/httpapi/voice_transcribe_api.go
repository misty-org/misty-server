package api

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"github.com/kannachi323/misty/server/internal/modelruntime"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Files up to the attachment reader's limit go to the runtime inline.
const voiceFileMaxBytes = 25_000_000

// VoiceTranscribeLibraryItem transcribes an audio or video file from a Space
// Library with speaker labels and saves the transcript. Opening the file
// needs Library view permission in that Space.
func (s *SpacesService) VoiceTranscribeLibraryItem() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		var body struct {
			SpaceID       string `json:"space_id"`
			LibraryItemID string `json:"library_item_id"`
			Language      string `json:"language"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if decodeAIJSON(w, r, &body) != nil || body.SpaceID == "" || body.LibraryItemID == "" || len(body.Language) > 16 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_transcribe"})
			return
		}
		if s.library == nil || s.models == nil || !s.models.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "transcribe_unavailable", "message": "Transcribing files isn't available right now."})
			return
		}
		transcript, err := s.transcribeLibraryItem(r.Context(), user, body.SpaceID, body.LibraryItemID, body.Language)
		if err != nil {
			writeTranscribeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, transcript)
	}
}

type voiceFileError string

func (e voiceFileError) Error() string { return string(e) }

func (s *SpacesService) transcribeLibraryItem(ctx context.Context, user, spaceID, itemID, language string) (db.VoiceTranscript, error) {
	audio, download, err := s.library.ReadExplicitAgentAttachment(ctx, user, spaceID, "library_item", itemID, voiceFileMaxBytes)
	if err != nil {
		return db.VoiceTranscript{}, err
	}
	mime := strings.ToLower(strings.TrimSpace(strings.Split(download.MIMEType, ";")[0]))
	if !strings.HasPrefix(mime, "audio/") && !strings.HasPrefix(mime, "video/") {
		return db.VoiceTranscript{}, voiceFileError("Only audio and video files can be transcribed.")
	}
	model := defaultRoleModel("diarized-transcription")
	operation := "voice-file:" + uuid.NewString()
	// Compressed speech runs well above 32 kbit/s, so this over-estimates
	// the length; settlement charges what was actually heard.
	estimate := int64(len(audio)) * 8 / 32
	if estimate > 4*60*60*1000 {
		estimate = 4 * 60 * 60 * 1000
	}
	if estimate < 1000 {
		estimate = 1000
	}
	billing := s.database.BillingService()
	reservation, err := billing.Reserve(ctx, billingadapter.Request{Version: 1, AccountID: user, Operation: liveVoiceOperation, OperationID: operation, Key: operation + ":reserve",
		Usage: billingadapter.Usage{Provider: "soniox", Model: model, Units: map[string]int64{"audio_ms": estimate}, Estimated: true}})
	if err != nil {
		return db.VoiceTranscript{}, err
	}
	result, err := s.models.TranscribeDiarized(ctx, model, mime, audio, languageHints(language), nil)
	if err != nil {
		_ = billing.Complete(ctx, "release", reservation, operation+":release", billingadapter.Usage{}, "provider_failed")
		return db.VoiceTranscript{}, err
	}
	heard := estimate
	if result.DurationInSeconds != nil {
		heard = int64(*result.DurationInSeconds * 1000)
	}
	if err = billing.Complete(ctx, "settle", reservation, operation+":settle", billingadapter.Usage{Provider: "soniox", Model: model, Units: map[string]int64{"audio_ms": heard}}, ""); err != nil {
		return db.VoiceTranscript{}, err
	}
	transcript := db.VoiceTranscript{
		ID: uuid.NewString(), Title: strings.TrimSuffix(download.Filename, path.Ext(download.Filename)),
		Source:   db.VoiceSource{Kind: "library_item", LibraryItemID: itemID, Title: download.Filename},
		Language: result.Language, Turns: voiceTurns(result.Turns), DurationMS: heard,
	}
	if len(transcript.Turns) == 0 {
		return db.VoiceTranscript{}, voiceFileError("No speech was found in that file.")
	}
	return transcript, s.database.SaveVoiceTranscript(ctx, user, transcript)
}

func languageHints(language string) []string {
	if language == "" || language == "auto" {
		return nil
	}
	return []string{language}
}

// voiceTurns names diarized speakers the way live captures do.
func voiceTurns(turns []modelruntime.Turn) []db.VoiceTurn {
	out := make([]db.VoiceTurn, 0, len(turns))
	for _, turn := range turns {
		if strings.TrimSpace(turn.Text) == "" {
			continue
		}
		out = append(out, db.VoiceTurn{Speaker: "Speaker " + firstNonEmpty(turn.Speaker, "?"), Language: turn.Language, Start: turn.Start, End: turn.End, Text: turn.Text})
	}
	return out
}

func writeTranscribeError(w http.ResponseWriter, err error) {
	var message voiceFileError
	if errors.As(err, &message) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "transcribe_rejected", "message": string(message)})
		return
	}
	writeVoiceError(w, err)
}
