package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

const noteProjectionBodyLimit = 512 * 1024

// JournalNoteProjection accepts a signed callback from the serialized Yjs
// room. It is intentionally separate from member-authenticated note routes.
func (s *SpacesService) JournalNoteProjection() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, noteProjectionBodyLimit+1))
		if err != nil || len(body) > noteProjectionBodyLimit {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		timestamp := r.Header.Get("X-Misty-Timestamp")
		issuedAt, parseErr := strconv.ParseInt(timestamp, 10, 64)
		if parseErr != nil || time.Since(time.Unix(issuedAt, 0)).Abs() > 5*time.Minute ||
			!s.TestingJournalCollab.VerifyProjectionSignature(timestamp, body, r.Header.Get("X-Misty-Signature")) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		var input struct {
			NoteID          string   `json:"note_id"`
			Revision        int64    `json:"revision"`
			Title           string   `json:"title"`
			Markdown        string   `json:"markdown"`
			PlainText       string   `json:"plain_text"`
			OutgoingNoteIDs []string `json:"outgoing_note_ids"`
		}
		if json.Unmarshal(body, &input) != nil {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		applied, err := s.database.ApplySpaceNoteProjection(r.Context(), db.SpaceNoteProjection{
			NoteID: input.NoteID, Revision: input.Revision, Title: input.Title,
			Markdown: input.Markdown, PlainText: input.PlainText, OutgoingNoteIDs: input.OutgoingNoteIDs,
		})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"applied": applied})
	}
}

// SpaceNotes handles membership-wide native Space notes.
func (s *SpacesService) SpaceNotes() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			notes, err := s.database.AccessibleSpaceNotes(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
		case http.MethodPost:
			var body struct {
				Title string `json:"title"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			note, err := s.database.CreateSpaceNote(r.Context(), userID, spaceID, body.Title)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, note)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// SpaceNote handles one note: reading it and deleting it. Both behave as
// not-found for any caller without the matching capability, so neither reveals
// that the note exists.
func (s *SpacesService) SpaceNote() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		noteID := chi.URLParam(r, "noteID")
		switch r.Method {
		case http.MethodGet:
			note, err := s.database.SpaceNoteByID(r.Context(), userID, noteID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			if note.SpaceID != chi.URLParam(r, "spaceID") {
				// The note exists but was requested under the wrong Space. Answer
				// as if it does not exist rather than confirming it elsewhere.
				writeSpaceError(w, db.ErrSpaceNotFound)
				return
			}
			writeJSON(w, http.StatusOK, note)
		case http.MethodDelete:
			if err := s.database.DeleteSpaceNote(r.Context(), userID, noteID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPatch:
			var body struct {
				Archived *bool `json:"archived"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			if body.Archived == nil {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			if err := s.database.SetSpaceNoteArchived(
				r.Context(), userID, noteID, *body.Archived,
			); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// SpaceNoteMetadata updates server-owned, non-CRDT metadata. Collaborative
// title and body changes never arrive here: they reach PostgreSQL only as
// projections from the collaboration service.
func (s *SpacesService) SpaceNoteMetadata() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			SharedTags []string `json:"shared_tags"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		noteID := chi.URLParam(r, "noteID")
		if err := s.database.UpdateNoteSharedTags(r.Context(), userID, noteID, body.SharedTags); err != nil {
			writeSpaceError(w, err)
			return
		}
		note, err := s.database.SpaceNoteByID(r.Context(), userID, noteID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, note)
	}
}

func (s *SpacesService) SpaceNoteBacklinks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		noteID := chi.URLParam(r, "noteID")
		note, err := s.database.SpaceNoteByID(r.Context(), userID, noteID)
		if err != nil || note.SpaceID != chi.URLParam(r, "spaceID") {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		links, err := s.database.SpaceNoteBacklinks(r.Context(), userID, noteID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"backlinks": links})
	}
}

// SpaceNoteAssets lists a note's assets and initiates a new asset upload.
//
// The note_attachment purpose is rejected by the generic Library upload
// endpoint, so this is the only route that can create one, and it authorizes
// against the parent note before reserving any quota.
func (s *SpaceLibraryService) SpaceNoteAssets() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		noteID := chi.URLParam(r, "noteID")
		if r.Method == http.MethodGet {
			assets, err := s.database.NoteAssets(r.Context(), userID, noteID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"assets": assets})
			return
		}
		if !s.TestingNoteAssetsEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "note_assets_disabled"})
			return
		}
		if !s.TestingDirectTransfersActive() {
			writeJSON(w, http.StatusServiceUnavailable,
				map[string]string{"code": "journal_asset_direct_transfer_required"})
			return
		}
		var body struct {
			Filename string `json:"filename"`
			MIMEType string `json:"mime_type"`
			ByteSize int64  `json:"byte_size"`
			SHA256   string `json:"sha256"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		body.Filename = sanitizeLibraryFilename(body.Filename)
		body.SHA256 = strings.ToLower(strings.TrimSpace(body.SHA256))
		body.MIMEType = strings.ToLower(strings.TrimSpace(body.MIMEType))
		maxBytes := s.TestingUploadLimits.Max(UploadPurposeNoteAttachment)
		if maxBytes < 1 || body.ByteSize < 1 || body.ByteSize > maxBytes ||
			!librarySHA256Pattern.MatchString(body.SHA256) || body.Filename == "" ||
			!TestingSupportedDrawingAssetMIME(body.MIMEType) {
			writeLibraryError(w, db.ErrLibraryInvalid)
			return
		}
		token, err := security.GenerateSecureToken()
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		objectKey := "library/" + strings.ReplaceAll(uuid.NewString(), "-", "")
		expiresAt := time.Now().Add(libraryUploadLifetime).UTC()
		upload, err := s.database.CreateNoteAssetUpload(r.Context(), userID, noteID, body.Filename,
			body.MIMEType, body.ByteSize, body.SHA256, objectKey, security.HashToken(token), expiresAt)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		transfer, err := s.TestingUploadTransfer(r.Context(), upload, token, expiresAt)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"upload":   upload,
			"transfer": transfer,
			"finalize": map[string]any{"headers": map[string]string{TestingLibraryUploadTokenHeader: token}},
		})
	}
}

// SpaceNoteAssetDownload issues an authorized download for one asset. Viewers
// may download; only uploading and removal require edit access.
func (s *SpaceLibraryService) SpaceNoteAssetDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		download, err := s.database.NoteAssetDownload(r.Context(), userID,
			chi.URLParam(r, "noteID"), chi.URLParam(r, "assetID"))
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		s.TestingWriteJournalAssetDownload(w, r, download)
	}
}

// SpaceNoteAsset removes one asset reference.
func (s *SpaceLibraryService) SpaceNoteAsset() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.DeleteNoteAsset(r.Context(), userID,
			chi.URLParam(r, "noteID"), chi.URLParam(r, "assetID")); err != nil {
			writeLibraryError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// SpaceNoteCollaborationTicket mints a short-lived, single-connection ticket
// for the collaboration service.
//
// Access is rechecked here rather than trusted from the note fetch that
// preceded it, because a grant can be revoked between the two calls.
func (s *SpacesService) SpaceNoteCollaborationTicket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		noteID := chi.URLParam(r, "noteID")
		access, err := s.database.RequireNoteView(r.Context(), userID, noteID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		// The note is re-read here rather than trusted from an earlier request,
		// because acl_version must be the one current at signing time. A ticket
		// carrying a stale version is refused by the room.
		note, err := s.database.SpaceNoteByID(r.Context(), userID, noteID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if note.SpaceID != chi.URLParam(r, "spaceID") {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		ticket, err := s.TestingJournalCollab.MintNoteTicket(userID, note.SpaceID, note.ID, access.Role, note.ACLVersion)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		// Tickets are bearer credentials with a 60 second life; no cache may
		// keep one around.
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusCreated, ticket)
	}
}
