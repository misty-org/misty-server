package api

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/collab"
	"github.com/misty-org/misty-server/internal/collab/admit"
	"github.com/misty-org/misty-server/internal/collab/wire"
)

func (t *CollabTransports) Mount(router chi.Router) {
	router.Get("/v1/collab", t.Socket())
	router.Post("/v1/collab/stream", t.Stream())
	router.Post("/v1/collab/stream/{conn}", t.StreamFrame())
	router.Get("/v1/collab/rooms/{spaceID}/{key}", t.ReadRoom())
	router.Post("/v1/collab/rooms/{spaceID}/{key}", t.ChangeRoom())
	router.Options("/v1/collab/*", t.Preflight())
}

func collabRoomKey(r *http.Request) (string, bool) {
	key := chi.URLParam(r, "key")
	if r.URL.RawPath != "" {
		decoded, err := url.PathUnescape(key)
		if err != nil {
			return "", false
		}
		key = decoded
	}
	return key, wire.ValidKey(key)
}

func collabStatus(err error) (int, string) {
	var moved *collab.Moved
	switch {
	case errors.As(err, &moved):
		return http.StatusTemporaryRedirect, "moved"
	case errors.Is(err, collab.ErrNoRoom):
		return http.StatusNotFound, "not_found"
	}
	code := admit.CodeOf(err)
	switch code {
	case wire.CloseBadOpen, wire.CloseBadFrame:
		return http.StatusBadRequest, wire.CloseName(code)
	case wire.CloseUnauthorized:
		return http.StatusUnauthorized, "unauthorized"
	case wire.CloseForbidden:
		return http.StatusForbidden, "forbidden"
	case wire.CloseNotFound:
		return http.StatusNotFound, "not_found"
	case wire.CloseOptionsConflict:
		return http.StatusConflict, "options_conflict"
	case wire.CloseTooLarge:
		return http.StatusRequestEntityTooLarge, "too_large"
	case wire.CloseGone:
		return http.StatusGone, "gone"
	}
	return http.StatusInternalServerError, "internal"
}

func writeCollabError(w http.ResponseWriter, r *http.Request, err error) {
	var moved *collab.Moved
	if errors.As(err, &moved) {
		w.Header().Set("Location", collabRedirect(moved.URL, r))
		w.WriteHeader(http.StatusTemporaryRedirect)
		return
	}
	status, code := collabStatus(err)
	writeJSON(w, status, map[string]string{"code": code})
}

func collabRedirect(base string, r *http.Request) string {
	base = strings.TrimRight(base, "/")
	switch {
	case strings.HasPrefix(base, "wss://"):
		base = "https://" + strings.TrimPrefix(base, "wss://")
	case strings.HasPrefix(base, "ws://"):
		base = "http://" + strings.TrimPrefix(base, "ws://")
	}
	return base + r.URL.RequestURI()
}

func (t *CollabTransports) ReadRoom() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		collabCORS(w)
		key, ok := collabRoomKey(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_open"})
			return
		}
		as := r.URL.Query().Get("as")
		if as != "" && as != "update" && as != "json" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
		content, err := t.Rooms.Read(r.Context(), bearerToken(r), chi.URLParam(r, "spaceID"), key, as == "json")
		if err != nil {
			writeCollabError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		if as == "json" {
			writeJSON(w, http.StatusOK, content.JSON)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(content.Update)))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content.Update)
	}
}

func (t *CollabTransports) ChangeRoom() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		collabCORS(w)
		key, ok := collabRoomKey(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_open"})
			return
		}
		if contentType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); contentType != "application/octet-stream" {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"code": "unsupported_media_type"})
			return
		}
		update, err := io.ReadAll(io.LimitReader(r.Body, wire.MaxFrameBytes+1))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_frame"})
			return
		}
		if len(update) > wire.MaxFrameBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "too_large"})
			return
		}
		var options []byte
		if raw := r.URL.Query().Get("options"); raw != "" {
			options = []byte(raw)
		}
		if err := t.Rooms.Change(r.Context(), bearerToken(r), chi.URLParam(r, "spaceID"), key, update, options); err != nil {
			writeCollabError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
