package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// Files up to the attachment reader's limit go to the desktop inline.
const voiceFileMaxBytes = 25_000_000

// VoiceTranscribeLibraryItem transcribes an audio or video file from a Space
// Library with speaker labels and saves the transcript. Opening the file
// needs Library view permission in that Space.
func (s *SpacesService) VoiceTranscribeLibraryItem() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			SpaceID       string `json:"space_id"`
			LibraryItemID string `json:"library_item_id"`
			Language      string `json:"language"`
			// A desktop transcribes on its own model and sends what it heard
			Result *diarizedResult `json:"result"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		if decodeStrictJSON(w, r, &body) != nil || body.SpaceID == "" || body.LibraryItemID == "" || len(body.Language) > 16 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_transcribe"})
			return
		}
		if s.library == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "transcribe_unavailable", "message": "Transcribing files isn't available right now."})
			return
		}
		s.transcribeLibraryItemOnDevice(w, r, user, body.SpaceID, body.LibraryItemID, body.Result)
	}
}

type voiceFileError string

func (e voiceFileError) Error() string { return string(e) }

// transcribeLibraryItemOnDevice answers a desktop in two steps: first the file to transcribe,
// then, with the desktop's result, the saved transcript.
func (s *SpacesService) transcribeLibraryItemOnDevice(w http.ResponseWriter, r *http.Request, user, spaceID, itemID string, result *diarizedResult) {
	audio, download, err := s.library.ReadLibraryItem(r.Context(), user, spaceID, itemID, voiceFileMaxBytes)
	if err != nil {
		writeTranscribeError(w, err)
		return
	}
	mime := strings.ToLower(strings.TrimSpace(strings.Split(download.MIMEType, ";")[0]))
	if !strings.HasPrefix(mime, "audio/") && !strings.HasPrefix(mime, "video/") {
		writeTranscribeError(w, voiceFileError("Only audio and video files can be transcribed."))
		return
	}
	if result == nil {
		writeJSON(w, http.StatusOK, map[string]any{"on_device": true, "mime_type": mime, "audio_base64": base64.StdEncoding.EncodeToString(audio)})
		return
	}
	transcript, err := s.saveLibraryTranscript(r.Context(), user, itemID, download.Filename, *result)
	if err != nil {
		writeTranscribeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, transcript)
}

func (s *SpacesService) saveLibraryTranscript(ctx context.Context, user, itemID, filename string, result diarizedResult) (db.VoiceTranscript, error) {
	if len(result.Turns) > 20_000 {
		return db.VoiceTranscript{}, voiceFileError("That transcript is too long.")
	}
	heard := int64(0)
	if result.DurationInSeconds != nil {
		heard = int64(*result.DurationInSeconds * 1000)
	}
	transcript := db.VoiceTranscript{
		ID: uuid.NewString(), Title: strings.TrimSuffix(filename, path.Ext(filename)),
		Source:   db.VoiceSource{Kind: "library_item", LibraryItemID: itemID, Title: filename},
		Language: result.Language, Turns: voiceTurns(result.Turns), DurationMS: heard,
	}
	if len(transcript.Turns) == 0 {
		return db.VoiceTranscript{}, voiceFileError("No speech was found in that file.")
	}
	return transcript, s.database.SaveVoiceTranscript(ctx, user, transcript)
}

// voiceTurns names diarized speakers the way live captures do.
func voiceTurns(turns []voiceTurnResult) []db.VoiceTurn {
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
