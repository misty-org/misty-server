package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// Transcripts are owner-only; they are never shared through a Space. A Note
// made from one lives in the person's own Space.
const voiceTranscriptBodyLimit = 8 << 20

// VoiceTranscripts saves a finished capture's diarized transcript.
func (s *SpacesService) VoiceTranscripts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		var body db.VoiceTranscript
		r.Body = http.MaxBytesReader(w, r.Body, voiceTranscriptBodyLimit)
		if decodeAIJSON(w, r, &body) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_transcript"})
			return
		}
		body.ID = uuid.NewString()
		body.Title = strings.TrimSpace(body.Title)
		if err := s.database.SaveVoiceTranscript(r.Context(), user, body); err != nil {
			writeVoiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": body.ID})
	}
}

// VoiceTranscript reads, renames speakers on, or deletes one transcript.
func (s *SpacesService) VoiceTranscript() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		id := chi.URLParam(r, "transcriptID")
		if _, err := uuid.Parse(id); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			transcript, err := s.database.VoiceTranscript(r.Context(), user, id)
			if err != nil {
				writeVoiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, transcript)
		case http.MethodPatch:
			// Speaker names, and the Library item holding the recording.
			var body struct {
				Speakers        map[string]string `json:"speakers"`
				RecordingItemID string            `json:"recording_item_id"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
			if decodeAIJSON(w, r, &body) != nil || (body.Speakers == nil && body.RecordingItemID == "") {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_speakers"})
				return
			}
			if err := s.database.UpdateVoiceTranscript(r.Context(), user, id, body.Speakers, "", body.RecordingItemID); err != nil {
				writeVoiceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			if err := s.database.DeleteVoiceTranscript(r.Context(), user, id); err != nil {
				writeVoiceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func writeVoiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrSpaceInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_transcript", "message": "That transcript couldn't be saved."})
	case errors.Is(err, db.ErrSpaceNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "That transcript is no longer available."})
	default:
		writeAgentError(w, err)
	}
}

// personalSpaceID is the person's own default Space, where voice Notes go.
func (s *SpacesService) personalSpaceID(ctx context.Context, user string) (string, error) {
	spaces, err := s.database.ListSpaces(ctx, user)
	if err != nil {
		return "", err
	}
	for _, space := range spaces {
		if space.IsDefault && space.Role == "owner" {
			return space.ID, nil
		}
	}
	return "", db.ErrSpaceNotFound
}
