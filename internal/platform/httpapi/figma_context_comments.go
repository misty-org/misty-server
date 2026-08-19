package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) FigmaBindingRecords() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items, err := s.database.FigmaContentRecords(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "bindingID"), r.URL.Query().Get("record_type"), r.URL.Query().Get("query"), limit)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"records": items})
	}
}

func (s *SpacesService) FigmaBindingContext() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		binding, err := s.database.FigmaBinding(r.Context(), userID, spaceID, chi.URLParam(r, "bindingID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		fileKey := binding.FileKey
		if binding.ResourceType == "project" {
			fileKey = strings.TrimSpace(r.URL.Query().Get("file_key"))
		}
		allowed, err := s.database.FigmaBindingContainsFile(r.Context(), binding, fileKey)
		if err != nil || !allowed {
			writeSpaceError(w, db.ErrSpaceForbidden)
			return
		}
		token, _, err := s.connectedAccountAccessTokenForCapability(r.Context(), binding.BoundByUserID, binding.ConnectionID, "drawings_read")
		if err != nil {
			_ = s.database.SetFigmaBindingSync(r.Context(), binding.ID, binding.SyncCursor, "needs_attention", "reauthorization_required")
			writeSpaceError(w, err)
			return
		}
		provider := s.figmaProvider(token)
		file, err := provider.File(r.Context(), fileKey)
		if err != nil {
			writeFigmaError(w, err)
			return
		}
		versions, err := provider.Versions(r.Context(), fileKey)
		if err != nil {
			writeFigmaError(w, err)
			return
		}
		comments, err := provider.Comments(r.Context(), fileKey)
		if err != nil {
			writeFigmaError(w, err)
			return
		}
		if len(versions) > 100 {
			versions = versions[:100]
		}
		if len(comments) > 200 {
			comments = comments[:200]
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"file": map[string]any{
				"key": file.Key, "name": file.Name, "version": file.Version,
				"last_modified": file.LastModified, "editor_type": file.EditorType,
				"thumbnail_url": file.ThumbnailURL, "document_summary": figmaDocumentSummary(file.Document),
			},
			"versions": versions, "comments": comments,
		})
	}
}

func (s *SpacesService) FigmaComments() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, bindingID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "bindingID")
		var body struct {
			FileKey        string `json:"file_key"`
			Message        string `json:"message"`
			NodeID         string `json:"node_id"`
			Confirmed      bool   `json:"confirmed"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		binding, err := s.database.FigmaBinding(r.Context(), userID, spaceID, bindingID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		fileKey := binding.FileKey
		if binding.ResourceType == "project" {
			fileKey = strings.TrimSpace(body.FileKey)
		}
		body.Message = strings.TrimSpace(body.Message)
		body.NodeID = strings.TrimSpace(body.NodeID)
		body.IdempotencyKey = firstNonempty(strings.TrimSpace(body.IdempotencyKey), strings.TrimSpace(r.Header.Get("Idempotency-Key")))
		if fileKey == "" || body.Message == "" || len([]rune(body.Message)) > 5000 || len(body.NodeID) > 256 {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		if err := s.database.RequireSpacePermission(r.Context(), userID, spaceID, db.PermissionIntegrationsManage); err != nil {
			writeSpaceError(w, err)
			return
		}
		allowed, err := s.database.FigmaBindingContainsFile(r.Context(), binding, fileKey)
		if err != nil || !allowed {
			writeSpaceError(w, db.ErrSpaceForbidden)
			return
		}
		if !body.Confirmed {
			_ = s.database.RecordFigmaCommentAudit(r.Context(), userID, spaceID, bindingID, "user", fileKey, body.NodeID, "figma_comment_confirmation_required", false, false)
			writeJSON(w, http.StatusConflict, map[string]string{"code": "figma_comment_confirmation_required"})
			return
		}
		if body.IdempotencyKey == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "figma_idempotency_key_required"})
			return
		}
		fingerprint := githubFingerprint(map[string]any{"file_key": fileKey, "message": body.Message, "node_id": body.NodeID})
		claimed, err := s.database.ClaimFigmaCommentAction(r.Context(), userID, spaceID, bindingID, "user", fileKey, body.NodeID, body.IdempotencyKey, fingerprint)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !claimed {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "figma_comment_already_claimed"})
			return
		}
		token, _, err := s.connectedAccountAccessTokenForCapability(r.Context(), binding.BoundByUserID, binding.ConnectionID, "drawings_comments")
		if err != nil {
			_ = s.database.FinishFigmaCommentAction(r.Context(), bindingID, body.IdempotencyKey, "reauthorization_required", false)
			writeSpaceError(w, err)
			return
		}
		comment, err := s.figmaProvider(token).PostComment(r.Context(), fileKey, body.Message, body.NodeID)
		if err != nil {
			_ = s.database.FinishFigmaCommentAction(r.Context(), bindingID, body.IdempotencyKey, "figma_api_error", false)
			writeFigmaError(w, err)
			return
		}
		_ = s.database.FinishFigmaCommentAction(r.Context(), bindingID, body.IdempotencyKey, "", true)
		_ = s.database.UpsertFigmaContentRecord(r.Context(), normalizeFigmaCommentRecord(binding.ID, fileKey, comment, "user"))
		writeJSON(w, http.StatusCreated, map[string]any{"comment": comment})
	}
}
