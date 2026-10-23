package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/db"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func (s *SpacesService) Nodes() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			nodes, err := s.database.SpaceNodes(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
			return
		}
		var body struct {
			ID          string          `json:"id"`
			ParentID    string          `json:"parent_id"`
			Kind        string          `json:"kind"`
			DisplayName string          `json:"display_name"`
			DriveURL    string          `json:"drive_url"`
			MIMEType    string          `json:"mime_type"`
			SizeBytes   *int64          `json:"size_bytes"`
			Metadata    json.RawMessage `json:"metadata"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		node := db.SpaceNode{SpaceID: spaceID, ParentID: body.ParentID, Kind: body.Kind, DisplayName: body.DisplayName, MIMEType: body.MIMEType, SizeBytes: body.SizeBytes, Metadata: body.Metadata}
		if body.Kind == "link" {
			parsed, err := ValidGoogleDriveTarget(body.DriveURL)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			node.TargetCipher, node.TargetNonce, err = s.EncryptTarget(parsed.String())
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			node.KeyVersion = s.keyVer
		}
		saved, err := s.database.UpsertSpaceNode(r.Context(), userID, node)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, saved)
	}
}

func (s *SpacesService) Node() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, nodeID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "nodeID")
		if r.Method == http.MethodDelete {
			if err := s.database.DeleteSpaceNode(r.Context(), userID, spaceID, nodeID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body struct {
			ParentID    string          `json:"parent_id"`
			DisplayName string          `json:"display_name"`
			Stale       bool            `json:"stale"`
			MIMEType    string          `json:"mime_type"`
			SizeBytes   *int64          `json:"size_bytes"`
			Metadata    json.RawMessage `json:"metadata"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		current, err := s.database.SpaceNodeSecret(r.Context(), userID, spaceID, nodeID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		current.ParentID, current.DisplayName, current.MIMEType, current.SizeBytes, current.Metadata = body.ParentID, body.DisplayName, body.MIMEType, body.SizeBytes, body.Metadata
		saved, err := s.database.UpsertSpaceNode(r.Context(), userID, *current)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if body.Stale && !saved.Stale {
			_ = s.database.MarkSpaceNodeStale(r.Context(), userID, spaceID, nodeID)
			saved.Stale = true
		}
		writeJSON(w, http.StatusOK, saved)
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
