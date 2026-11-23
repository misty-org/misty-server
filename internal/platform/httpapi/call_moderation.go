package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

type callModeration struct {
	Action string `json:"action"`
	Person string `json:"person"`
}

func (w *LiveKitRoomWatcher) Moderate(database *db.Database) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(rw, r, database)
		if !ok {
			return
		}
		if w == nil {
			writeJSON(rw, http.StatusServiceUnavailable, map[string]string{"code": "calls_unavailable"})
			return
		}
		room, err := url.PathUnescape(chi.URLParam(r, "room"))
		if err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]string{"code": "invalid_room"})
			return
		}
		var body callModeration
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil ||
			body.Person == "" || (body.Action != "mute" && body.Action != "remove") {
			writeJSON(rw, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
			return
		}
		if err := database.RequireCallModerator(r.Context(), userID, room, body.Person); err != nil {
			writeSpaceError(rw, err)
			return
		}
		if body.Action == "mute" {
			err = w.muteMicrophone(r.Context(), room, body.Person)
		} else {
			err = w.call(r.Context(), "RemoveParticipant", room,
				map[string]any{"room": room, "identity": body.Person}, nil)
		}
		if err != nil {
			writeJSON(rw, http.StatusBadGateway, map[string]string{"code": "call_unreachable"})
			return
		}
		rw.WriteHeader(http.StatusNoContent)
	}
}

type liveKitTrack struct {
	SID    string          `json:"sid"`
	Source json.RawMessage `json:"source"`
	Muted  bool            `json:"muted"`
}

func (t liveKitTrack) microphone() bool {
	source := bytes.TrimSpace(t.Source)
	return bytes.Equal(source, []byte(`"MICROPHONE"`)) || bytes.Equal(source, []byte(`2`))
}

func (w *LiveKitRoomWatcher) muteMicrophone(ctx context.Context, room, identity string) error {
	var found struct {
		Tracks []liveKitTrack `json:"tracks"`
	}
	if err := w.call(ctx, "GetParticipant", room,
		map[string]any{"room": room, "identity": identity}, &found); err != nil {
		return err
	}
	for _, track := range found.Tracks {
		if !track.microphone() || track.Muted {
			continue
		}
		if err := w.call(ctx, "MutePublishedTrack", room, map[string]any{
			"room": room, "identity": identity, "track_sid": track.SID, "muted": true,
		}, nil); err != nil {
			return err
		}
	}
	return nil
}
