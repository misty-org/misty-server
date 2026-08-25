package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const (
	maxMistyAttachmentBytes = int64(10 << 20)
	maxMistyModelImageBytes = int64(1 << 20)
)

type mistyAttachmentInitiateInput struct {
	ConversationID string `json:"conversation_id"`
	Scope          string `json:"scope"`
	Filename       string `json:"filename"`
	MIMEType       string `json:"mime_type"`
	ByteSize       int64  `json:"byte_size"`
	SHA256         string `json:"sha256"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	ModelMIMEType  string `json:"model_mime_type"`
	ModelByteSize  int64  `json:"model_byte_size"`
	ModelSHA256    string `json:"model_sha256"`
	ModelWidth     int    `json:"model_width"`
	ModelHeight    int    `json:"model_height"`
}

func (s *AIService) MistyAttachments() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if s.attachmentStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "attachment_storage_unavailable", "message": "Image attachments are temporarily unavailable."})
			return
		}
		if keys, cleanupErr := s.database.DeleteExpiredAIConversationAttachments(r.Context(), userID); cleanupErr == nil {
			for _, key := range keys {
				_ = s.attachmentStore.Delete(r.Context(), key)
			}
		}
		var body mistyAttachmentInitiateInput
		if decodeAIJSON(w, r, &body) != nil || validateMistyAttachmentInput(body) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_attachment", "message": "Choose a JPEG, PNG, or WebP image up to 10 MB."})
			return
		}
		if body.Scope == "conversation" && strings.TrimSpace(body.ConversationID) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "conversation_required", "message": "Create a conversation before attaching images."})
			return
		}
		id := "aiatt_" + uuid.NewString()
		now := time.Now().UTC()
		item := db.AIConversationAttachment{
			ID: id, UserID: userID, ConversationID: strings.TrimSpace(body.ConversationID), Scope: body.Scope,
			DisplayName: sanitizeLibraryFilename(body.Filename), MIMEType: body.MIMEType, ByteSize: body.ByteSize,
			SHA256: body.SHA256, Width: body.Width, Height: body.Height, ObjectKey: "library/" + id,
			ModelMIMEType: body.ModelMIMEType, ModelByteSize: body.ModelByteSize, ModelSHA256: body.ModelSHA256,
			ModelWidth: body.ModelWidth, ModelHeight: body.ModelHeight, ModelObjectKey: "library/" + id + "_model",
		}
		if item.DisplayName == "" {
			item.DisplayName = "Misty image"
		}
		if body.Scope == "visual_query" {
			expires := now.Add(time.Hour)
			item.ExpiresAt = &expires
		}
		if err := s.database.CreateAIConversationAttachment(r.Context(), item); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "attachment_limit", "message": err.Error()})
			return
		}
		original, err := s.mistyAttachmentTransfer(r, item, false)
		if err != nil {
			_, _ = s.database.DeleteAIConversationAttachment(r.Context(), userID, item.ID)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "upload_unavailable", "message": "Misty could not prepare that upload."})
			return
		}
		model, err := s.mistyAttachmentTransfer(r, item, true)
		if err != nil {
			_, _ = s.database.DeleteAIConversationAttachment(r.Context(), userID, item.ID)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "upload_unavailable", "message": "Misty could not prepare that upload."})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"attachment": mistyAttachmentDTO(item), "original_transfer": original, "model_transfer": model})
	}
}

func validateMistyAttachmentInput(body mistyAttachmentInitiateInput) error {
	if body.Scope != "conversation" && body.Scope != "visual_query" {
		return errors.New("invalid scope")
	}
	if body.MIMEType != "image/jpeg" && body.MIMEType != "image/png" && body.MIMEType != "image/webp" {
		return errors.New("invalid mime")
	}
	if body.ModelMIMEType != "image/jpeg" && body.ModelMIMEType != "image/png" && body.ModelMIMEType != "image/webp" {
		return errors.New("invalid model mime")
	}
	if body.ByteSize < 1 || body.ByteSize > maxMistyAttachmentBytes || body.ModelByteSize < 1 || body.ModelByteSize > maxMistyModelImageBytes {
		return errors.New("invalid size")
	}
	if !librarySHA256Pattern.MatchString(body.SHA256) || !librarySHA256Pattern.MatchString(body.ModelSHA256) {
		return errors.New("invalid checksum")
	}
	if body.Width < 1 || body.Width > 16384 || body.Height < 1 || body.Height > 16384 || body.ModelWidth < 1 || body.ModelWidth > 2048 || body.ModelHeight < 1 || body.ModelHeight > 2048 {
		return errors.New("invalid dimensions")
	}
	return nil
}

func (s *AIService) mistyAttachmentTransfer(r *http.Request, item db.AIConversationAttachment, model bool) (PresignedTransfer, error) {
	key, mimeType, byteSize, checksum := item.ObjectKey, item.MIMEType, item.ByteSize, item.SHA256
	variant := "original"
	if model {
		key, mimeType, byteSize, checksum, variant = item.ModelObjectKey, item.ModelMIMEType, item.ModelByteSize, item.ModelSHA256, "model"
	}
	metadata := LibraryObjectMetadata{ByteSize: byteSize, SHA256: checksum, MIMEType: mimeType}
	if s.attachmentPresigner != nil {
		return s.attachmentPresigner.PresignPut(r.Context(), key, metadata, s.attachmentUploadTTL)
	}
	return PresignedTransfer{URL: "/misty/attachments/" + item.ID + "/content?variant=" + variant, Method: http.MethodPut, Headers: map[string]string{}, ExpiresAt: time.Now().Add(s.attachmentUploadTTL).UTC()}, nil
}

func (s *AIService) MistyAttachment() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		id := strings.TrimSpace(chi.URLParam(r, "attachmentID"))
		item, err := s.database.AIConversationAttachment(r.Context(), userID, id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		switch r.Method {
		case http.MethodPost:
			if item.LifecycleState != "pending" {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "already_finalized"})
				return
			}
			original, originalErr := s.attachmentStore.Head(r.Context(), item.ObjectKey)
			model, modelErr := s.attachmentStore.Head(r.Context(), item.ModelObjectKey)
			if originalErr != nil || modelErr != nil || !mistyObjectMatches(original, item.MIMEType, item.ByteSize, item.SHA256) || !mistyObjectMatches(model, item.ModelMIMEType, item.ModelByteSize, item.ModelSHA256) {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "upload_incomplete", "message": "The image upload did not complete."})
				return
			}
			completed, completeErr := s.database.CompleteAIConversationAttachment(r.Context(), userID, id)
			if completeErr != nil {
				TestingWriteAIError(w, completeErr)
				return
			}
			writeJSON(w, http.StatusOK, mistyAttachmentDTO(*completed))
		case http.MethodDelete:
			deleted, deleteErr := s.database.DeleteAIConversationAttachment(r.Context(), userID, id)
			if deleteErr != nil {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "attachment_in_use", "message": deleteErr.Error()})
				return
			}
			_ = s.attachmentStore.Delete(r.Context(), deleted.ObjectKey)
			_ = s.attachmentStore.Delete(r.Context(), deleted.ModelObjectKey)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func mistyObjectMatches(value LibraryObjectMetadata, mimeType string, byteSize int64, checksum string) bool {
	return value.MIMEType == mimeType && value.ByteSize == byteSize && strings.EqualFold(value.SHA256, checksum)
}

func (s *AIService) MistyAttachmentContent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		item, err := s.database.AIConversationAttachment(r.Context(), userID, chi.URLParam(r, "attachmentID"))
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		model := r.URL.Query().Get("variant") == "model"
		key, mimeType, byteSize, checksum := item.ObjectKey, item.MIMEType, item.ByteSize, item.SHA256
		if model {
			key, mimeType, byteSize, checksum = item.ModelObjectKey, item.ModelMIMEType, item.ModelByteSize, item.ModelSHA256
		}
		if r.Method == http.MethodPut {
			if item.LifecycleState != "pending" {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "already_finalized"})
				return
			}
			hasher := sha256.New()
			reader := io.TeeReader(io.LimitReader(r.Body, byteSize+1), hasher)
			if err := s.attachmentStore.Put(r.Context(), key, reader, LibraryObjectMetadata{ByteSize: byteSize, SHA256: checksum, MIMEType: mimeType}); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_upload", "message": "The uploaded image did not match its checksum."})
				return
			}
			if hex.EncodeToString(hasher.Sum(nil)) != checksum {
				_ = s.attachmentStore.Delete(r.Context(), key)
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_upload"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet || item.LifecycleState != "ready" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if s.attachmentPresigner != nil {
			download, signErr := s.attachmentPresigner.PresignGet(r.Context(), key, item.DisplayName, s.attachmentDownloadTTL)
			if signErr == nil {
				http.Redirect(w, r, download.URL, http.StatusTemporaryRedirect)
				return
			}
		}
		reader, metadata, openErr := s.attachmentStore.Open(r.Context(), key)
		if openErr != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		defer reader.Close()
		w.Header().Set("Content-Type", metadata.MIMEType)
		w.Header().Set("Cache-Control", "private, max-age=60")
		_, _ = io.Copy(w, io.LimitReader(reader, metadata.ByteSize))
	}
}

func mistyAttachmentDTO(item db.AIConversationAttachment) map[string]any {
	return map[string]any{"id": item.ID, "name": item.DisplayName, "mime_type": item.MIMEType, "byte_size": item.ByteSize, "width": item.Width, "height": item.Height, "state": item.LifecycleState, "preview_url": "/misty/attachments/" + item.ID + "/content?variant=model"}
}
