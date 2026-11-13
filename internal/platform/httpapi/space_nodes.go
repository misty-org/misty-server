package api

import (
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/security"
)

// Nodes answers GET /spaces/{spaceID}/nodes: the Drive links shared into the
// Space. Nothing creates or changes one any more; older messages still name them.
func (s *SpacesService) Nodes() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		nodes, err := s.database.SpaceNodes(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
	}
}

// Node answers DELETE /spaces/{spaceID}/nodes/{nodeID}.
func (s *SpacesService) Node() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.DeleteSpaceNode(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "nodeID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func randomToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, security.HashToken(token), nil
}

func (s *SpacesService) ResolveTicket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Disposition string `json:"disposition"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if body.Disposition == "" {
			body.Disposition = "open"
		}
		token, hash, err := randomToken()
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if err := s.database.CreateResolveTicket(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "nodeID"), body.Disposition, hash, time.Now().UTC().Add(60*time.Second)); err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ticket": token, "url": "/spaces/resolve/" + url.PathEscape(token), "expires_in": 60})
	}
}

func (s *SpacesService) Resolve() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := chi.URLParam(r, "ticket")
		userID, spaceID, nodeID, err := s.database.ConsumeResolveTicket(r.Context(), security.HashToken(token))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		node, err := s.database.SpaceNodeSecret(r.Context(), userID, spaceID, nodeID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		target, err := s.DecryptTarget(node.TargetCipher, node.TargetNonce)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		parsed, err := ValidGoogleDriveTarget(target)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		http.Redirect(w, r, parsed.String(), http.StatusFound)
	}
}
