package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// Drive items over HTTP: the change feed, one item, creating, changing,
// trashing, restoring and purging items, the trash, and a file's download.

// DriveChanges is GET /spaces/{spaceID}/drive/changes?since=&limit=.
func (s *SpaceDriveService) DriveChanges() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		query := r.URL.Query()
		since, limit := int64(0), db.MaxDriveChangesPage
		if raw := query.Get("since"); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed < 0 {
				writeDriveError(w, db.ErrLibraryInvalid)
				return
			}
			since = parsed
		}
		if raw := query.Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > db.MaxDriveChangesPage {
				writeDriveError(w, db.ErrLibraryInvalid)
				return
			}
			limit = parsed
		}
		page, err := s.database.DriveChanges(r.Context(), userID, chi.URLParam(r, "spaceID"), since, limit)
		if err != nil {
			writeDriveError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusOK, page)
	}
}

// DriveItem is GET /spaces/{spaceID}/drive/items/{itemID}.
func (s *SpaceDriveService) DriveItem() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		item, err := s.database.DriveItem(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID"))
		if err != nil {
			writeDriveError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusOK, item)
	}
}

type driveCreateBody struct {
	ID       string  `json:"id"`
	ParentID *string `json:"parent_id"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	SHA256   *string `json:"sha256"`
	Size     *int64  `json:"size"`
	MTimeMS  *int64  `json:"mtime_ms"`
	UploadID string  `json:"upload_id"`
}

// CreateDriveItem is POST /spaces/{spaceID}/drive/items: 201 with the new
// item, or 200 with the same item when a create naming its own id is retried.
// A trashed folder it goes into comes back first (revived: true).
func (s *SpaceDriveService) CreateDriveItem() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		device, ok := driveDevice(w, r)
		if !ok {
			return
		}
		var body driveCreateBody
		if decodeJSON(w, r, &body) != nil {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		in := db.DriveCreateInput{
			UserID: userID, SpaceID: spaceID, Device: device, ID: strings.TrimSpace(body.ID),
			ParentID: body.ParentID, Name: body.Name, Kind: body.Kind, UploadID: strings.TrimSpace(body.UploadID),
		}
		if body.SHA256 != nil {
			in.SHA256 = strings.ToLower(strings.TrimSpace(*body.SHA256))
		}
		if body.Size != nil {
			in.Size = *body.Size
		}
		if body.MTimeMS != nil {
			in.MTimeMS = *body.MTimeMS
		}
		if in.Kind == "file" && (body.SHA256 == nil || body.Size == nil) {
			writeDriveError(w, db.ErrLibraryInvalid)
			return
		}
		s.landDriveUpload(r, userID, spaceID, in.UploadID)
		result, err := s.database.CreateDriveItem(r.Context(), in)
		if err != nil {
			writeDriveError(w, err)
			return
		}
		s.cleanUpDriveObjects(r.Context(), result.DeleteObjectKeys, nil)
		status := http.StatusCreated
		if !result.Created {
			status = http.StatusOK
		}
		writeJSON(w, status, driveCreated{DriveItem: result.Item, Revived: result.Revived})
	}
}

type drivePatchBody struct {
	Name        *string         `json:"name"`
	ParentID    json.RawMessage `json:"parent_id"`
	SHA256      *string         `json:"sha256"`
	Size        *int64          `json:"size"`
	MTimeMS     *int64          `json:"mtime_ms"`
	UploadID    *string         `json:"upload_id"`
	BaseVersion *int64          `json:"base_version"`
	Replace     bool            `json:"replace"`
}

// PatchDriveItem is PATCH /spaces/{spaceID}/drive/items/{itemID}: a new name,
// parent (null for the root), content or mtime. If-Match is optional: sent, a
// stale one is 409 version_conflict; left out, the write applies. base_version
// is the version the sender's copy came from, so content it never saw is kept
// as a trashed copy (replaced); replace lets a new name take one a live item
// holds, trashing that item first. A trashed item comes back (revived: true).
func (s *SpaceDriveService) PatchDriveItem() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		version, ok := driveIfMatch(w, r)
		if !ok {
			return
		}
		device, ok := driveDevice(w, r)
		if !ok {
			return
		}
		var body drivePatchBody
		if decodeJSON(w, r, &body) != nil {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		in := db.DrivePatchInput{
			UserID: userID, SpaceID: spaceID, ItemID: chi.URLParam(r, "itemID"), Device: device,
			IfMatch: version, Name: body.Name, MTimeMS: body.MTimeMS, Replace: body.Replace,
		}
		if body.BaseVersion != nil {
			if *body.BaseVersion < 1 {
				writeDriveError(w, db.ErrLibraryInvalid)
				return
			}
			in.BaseVersion = *body.BaseVersion
		}
		if len(body.ParentID) > 0 {
			in.MoveParent = true
			if !bytes.Equal(bytes.TrimSpace(body.ParentID), []byte("null")) {
				var parent string
				if json.Unmarshal(body.ParentID, &parent) != nil || parent == "" {
					writeDriveError(w, db.ErrLibraryInvalid)
					return
				}
				in.ParentID = &parent
			}
		}
		if body.SHA256 != nil || body.Size != nil || body.UploadID != nil {
			if body.SHA256 == nil || body.Size == nil {
				writeDriveError(w, db.ErrLibraryInvalid)
				return
			}
			in.Content, in.SHA256, in.Size = true, strings.ToLower(strings.TrimSpace(*body.SHA256)), *body.Size
			if body.UploadID != nil {
				in.UploadID = strings.TrimSpace(*body.UploadID)
			}
		}
		s.landDriveUpload(r, userID, spaceID, in.UploadID)
		result, err := s.database.PatchDriveItem(r.Context(), in)
		if err != nil {
			writeDriveError(w, err)
			return
		}
		s.cleanUpDriveObjects(r.Context(), result.DeleteObjectKeys, result.RetireKeys)
		writeJSON(w, http.StatusOK, driveChanged{DriveItem: result.Item, Revived: result.Revived, Replaced: result.Replaced})
	}
}

// TrashDriveItem is POST /spaces/{spaceID}/drive/items/{itemID}/trash with
// {held}. If-Match is optional: a stale one is 409 version_conflict. It
// answers 423 while the item, or anything in a folder, is open on another
// computer.
func (s *SpaceDriveService) TrashDriveItem() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		version, ok := driveIfMatch(w, r)
		if !ok {
			return
		}
		device, ok := driveDevice(w, r)
		if !ok {
			return
		}
		var body struct {
			Held bool `json:"held"`
		}
		if decodeOptionalJSON(w, r, &body, maxJSONBodyBytes) != nil {
			return
		}
		result, err := s.database.TrashDriveItem(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID"), device, version, body.Held)
		if err != nil {
			writeDriveError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result.Item)
	}
}

// RestoreDriveItem is POST /spaces/{spaceID}/drive/items/{itemID}/restore. An
// If-Match is optional here: restoring changes nothing anyone could have lost.
func (s *SpaceDriveService) RestoreDriveItem() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		device, ok := driveDevice(w, r)
		if !ok {
			return
		}
		version := int64(0)
		if header := r.Header.Get("If-Match"); strings.TrimSpace(header) != "" {
			if version, ok = parseDriveVersion(header); !ok {
				writeDriveError(w, db.ErrLibraryInvalid)
				return
			}
		}
		result, err := s.database.RestoreDriveItem(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID"), device, version)
		if err != nil {
			writeDriveError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result.Item)
	}
}

// PurgeDriveItem is DELETE /spaces/{spaceID}/drive/items/{itemID}: a trashed
// item and everything under it go for good. It needs drive.purge.
func (s *SpaceDriveService) PurgeDriveItem() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		device, ok := driveDevice(w, r)
		if !ok {
			return
		}
		retire, err := s.database.PurgeDriveItem(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID"), device)
		if err != nil {
			writeDriveError(w, err)
			return
		}
		s.cleanUpDriveObjects(r.Context(), nil, retire)
		w.WriteHeader(http.StatusNoContent)
	}
}

// DriveTrash is GET /spaces/{spaceID}/drive/trash?after=&limit=.
func (s *SpaceDriveService) DriveTrash() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 200 {
				writeDriveError(w, db.ErrLibraryInvalid)
				return
			}
			limit = parsed
		}
		page, err := s.database.DriveTrash(r.Context(), userID, chi.URLParam(r, "spaceID"), r.URL.Query().Get("after"), limit)
		if err != nil {
			writeDriveError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusOK, page)
	}
}

// DriveContent is GET /spaces/{spaceID}/drive/items/{itemID}/content: one
// presigned download of a file, valid for 15 minutes. An empty file has no
// bytes to fetch, so its URL is empty.
func (s *SpaceDriveService) DriveContent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		item, err := s.database.DriveItem(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID"))
		if err != nil {
			writeDriveError(w, err)
			return
		}
		if item.Deleted || item.Kind != "file" {
			writeDriveError(w, db.ErrDriveNotFound)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		if item.BlobKey == "" {
			writeJSON(w, http.StatusOK, map[string]any{"url": "", "expires_at": time.Now().UTC().Add(driveDownloadURLTTL)})
			return
		}
		if s.Presigner == nil {
			writeDriveError(w, ErrLibraryDirectTransferUnavailable)
			return
		}
		signed, err := s.Presigner.PresignGet(r.Context(), item.BlobKey, item.Name, driveDownloadURLTTL)
		if err != nil {
			writeDriveError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"url": signed.URL, "expires_at": signed.ExpiresAt})
	}
}
