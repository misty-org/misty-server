package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type uploadedAgentAvatar struct {
	Kind    string `json:"kind"`
	AssetID string `json:"asset_id"`
	Version int64  `json:"version"`
}

func agentAvatarObjectKey(assetID string) string { return "avatars/" + assetID }

func (s *AgentsService) PersonalAgentAvatar() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		version, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("version")), 10, 64)
		if err != nil || version < 1 {
			writeAgentError(w, db.ErrSpaceInvalid)
			return
		}
		avatar, err := s.database.PersonalAgentAvatarForUser(r.Context(), userID, agentID, version)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		var uploaded uploadedAgentAvatar
		if json.Unmarshal(avatar, &uploaded) != nil || uploaded.Kind != "upload" || uploaded.AssetID == "" || uploaded.Version != version {
			writeAgentError(w, db.ErrPersonalAgentNotFound)
			return
		}
		s.servePersonalAgentAvatar(w, r, uploaded)
	}
}

func (s *AgentsService) servePersonalAgentAvatar(w http.ResponseWriter, r *http.Request, avatar uploadedAgentAvatar) {
	if s.avatarStore == nil {
		http.Error(w, "avatar not found", http.StatusNotFound)
		return
	}
	reader, metadata, err := s.avatarStore.Open(r.Context(), agentAvatarObjectKey(avatar.AssetID))
	if err != nil {
		if errors.Is(err, ErrLibraryObjectNotFound) {
			http.Error(w, "avatar not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", metadata.MIMEType)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("ETag", `"agent-avatar-`+avatar.AssetID+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}
