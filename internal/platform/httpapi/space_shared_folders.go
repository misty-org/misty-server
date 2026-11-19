package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
)

// Shared Space folders over HTTP (db/space_shared_folders.go has the model).
// The server only tracks which blobs make up which folder in which Space:
// file bytes never pass through it. A diff hands out presigned R2 PUTs and a
// manifest presigned R2 GETs. With a store that can't sign (local development
// without R2), both refuse rather than route bytes through the server.

const (
	// A 50,000-file manifest of long paths stays under this.
	maxSharedFolderBodyBytes = 64 << 20
	// sharedFolderStoreConcurrency bounds object store calls per request.
	sharedFolderStoreConcurrency = 16
)

type sharedFolderFilesBody struct {
	Files []db.SharedFolderFile `json:"files"`
}

type sharedFolderUploadTarget struct {
	SHA256  string            `json:"sha256"`
	Size    int64             `json:"size"`
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
}

type sharedFolderManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	URL    string `json:"url"`
}

func writeSharedFolderError(w http.ResponseWriter, err error) {
	var missing db.SharedFolderBlobsMissingError
	switch {
	case errors.As(err, &missing):
		writeJSON(w, http.StatusConflict, map[string]any{"code": "folder_blobs_missing", "missing": missing.SHA256})
	case errors.Is(err, db.ErrLibraryNotFound), errors.Is(err, ErrLibraryObjectNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
	case errors.Is(err, db.ErrLibraryConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "version_conflict"})
	default:
		writeSpaceError(w, err)
	}
}

// SharedFolders lists a Space's folders (GET) or starts one (POST).
func (s *SpaceLibraryService) SharedFolders() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			folders, err := s.database.SharedFolders(r.Context(), userID, spaceID)
			if err != nil {
				writeSharedFolderError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"folders": folders})
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		folder, err := s.database.CreateSharedFolder(r.Context(), userID, spaceID, body.Name)
		if err != nil {
			writeSharedFolderError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, folder)
	}
}

// DeleteSharedFolder removes a folder and frees its bytes.
func (s *SpaceLibraryService) DeleteSharedFolder() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		deleteKeys, retireKeys, err := s.database.DeleteSharedFolder(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "folderID"))
		if err != nil {
			writeSharedFolderError(w, err)
			return
		}
		s.cleanUpSharedFolderObjects(r.Context(), deleteKeys, retireKeys)
		w.WriteHeader(http.StatusNoContent)
	}
}

// SharedFolderDiff answers which blobs of a manifest the server lacks, with a
// transfer for each, and which committed paths the manifest drops.
func (s *SpaceLibraryService) SharedFolderDiff() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, folderID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "folderID")
		if !s.UploadsEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_uploads_disabled"})
			return
		}
		if !s.requireFolderDirectTransfer(w) {
			return
		}
		files, ok := decodeSharedFolderManifest(w, r)
		if !ok {
			return
		}
		pending, err := s.database.PendingSharedFolderUploads(r.Context(), userID, spaceID, folderID)
		if err != nil {
			writeSharedFolderError(w, err)
			return
		}
		// A presigned PUT leaves no trace in the database, so an upload from an
		// earlier diff counts as sent only once its object is in the store.
		landed := s.landedSharedFolderUploads(r.Context(), pending, files)
		token, err := security.GenerateSecureToken()
		if err != nil {
			writeSharedFolderError(w, err)
			return
		}
		expiresAt := time.Now().Add(libraryUploadLifetime).UTC()
		plan, err := s.database.PlanSharedFolderUpload(r.Context(), db.SharedFolderPlanInput{
			UserID: userID, SpaceID: spaceID, FolderID: folderID, Files: files,
			Landed: landed, TokenHash: security.HashToken(token), ExpiresAt: expiresAt,
		})
		if err != nil {
			writeSharedFolderError(w, err)
			return
		}
		s.cleanUpSharedFolderObjects(r.Context(), plan.ReleasedObjectKeys, nil)
		uploads := make([]sharedFolderUploadTarget, 0, len(plan.Uploads))
		for _, upload := range plan.Uploads {
			transfer, err := s.UploadTransfer(r.Context(), &db.LibraryUpload{
				ID: upload.ID, SpaceID: spaceID, ObjectKey: upload.ObjectKey,
				RequestedByteSize: upload.ByteSize, ClientSHA256: upload.SHA256,
				ClientDeclaredMIMEType: db.SharedFolderMIMEType,
			}, token, expiresAt)
			if err != nil {
				writeSharedFolderError(w, err)
				return
			}
			uploads = append(uploads, sharedFolderUploadTarget{
				SHA256: upload.SHA256, Size: upload.ByteSize,
				URL: transfer.URL, Method: transfer.Method, Headers: transfer.Headers,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"upload": uploads, "remove": plan.Remove, "reused": plan.Reused})
	}
}

// SharedFolderCommit replaces a folder's manifest once every blob is present.
func (s *SpaceLibraryService) SharedFolderCommit() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, folderID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "folderID")
		if !s.UploadsEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_uploads_disabled"})
			return
		}
		files, ok := decodeSharedFolderManifest(w, r)
		if !ok {
			return
		}
		pending, err := s.database.PendingSharedFolderUploads(r.Context(), userID, spaceID, folderID)
		if err != nil {
			writeSharedFolderError(w, err)
			return
		}
		// The store is the only proof a blob landed: same key, size and digest.
		verified := s.landedSharedFolderUploads(r.Context(), pending, files)
		result, err := s.database.CommitSharedFolder(r.Context(), userID, spaceID, folderID, files, verified)
		if err != nil {
			writeSharedFolderError(w, err)
			return
		}
		s.cleanUpSharedFolderObjects(r.Context(), result.DeleteObjectKeys, result.RetireKeys)
		writeJSON(w, http.StatusOK, result.Folder)
	}
}

// SharedFolderManifest returns a folder's files with a download URL each. An
// empty file has no bytes to fetch, so its URL is empty.
func (s *SpaceLibraryService) SharedFolderManifest() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if !s.requireFolderDirectTransfer(w) {
			return
		}
		folder, files, err := s.database.SharedFolderManifest(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "folderID"))
		if err != nil {
			writeSharedFolderError(w, err)
			return
		}
		out := make([]sharedFolderManifestFile, len(files))
		for i, file := range files {
			out[i] = sharedFolderManifestFile{Path: file.Path, SHA256: file.SHA256, Size: file.Size}
			if file.BlobKey == "" {
				continue
			}
			signed, err := s.Presigner.PresignGet(r.Context(), file.BlobKey, path.Base(file.Path), s.Transfers.DownloadURLTTL)
			if err != nil {
				writeSharedFolderError(w, err)
				return
			}
			out[i].URL = signed.URL
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusOK, map[string]any{"folder": folder, "files": out})
	}
}

// requireFolderDirectTransfer refuses a folder transfer when the store can't
// sign R2 URLs: folder bytes go straight between the app and R2, never through
// the server.
func (s *SpaceLibraryService) requireFolderDirectTransfer(w http.ResponseWriter) bool {
	if s.DirectTransfersActive() {
		return true
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "folder_direct_transfer_unavailable"})
	return false
}

func decodeSharedFolderManifest(w http.ResponseWriter, r *http.Request) ([]db.SharedFolderFile, bool) {
	var body sharedFolderFilesBody
	if decodeJSONUpTo(w, r, &body, maxSharedFolderBodyBytes) != nil {
		return nil, false
	}
	files, err := db.NormalizeSharedFolderManifest(body.Files)
	if err != nil {
		writeSharedFolderError(w, err)
		return nil, false
	}
	return files, true
}

// landedSharedFolderUploads HEADs the caller's pending uploads that the
// manifest still needs and returns those whose object matches exactly: a
// presigned upload still marked initiated, or one an earlier diff saw land.
func (s *SpaceLibraryService) landedSharedFolderUploads(ctx context.Context, pending []db.SharedFolderPendingUpload, files []db.SharedFolderFile) []string {
	needed := map[string]bool{}
	for _, file := range files {
		needed[fmt.Sprint(file.SHA256, ":", file.Size)] = true
	}
	candidates := []db.SharedFolderPendingUpload{}
	for _, upload := range pending {
		if !needed[fmt.Sprint(upload.SHA256, ":", upload.ByteSize)] {
			continue
		}
		// Initiated: the presigned PUT may have landed since. Uploaded but
		// unverified: an earlier diff saw it land; its object is checked again.
		if upload.State == "initiated" || upload.State == "uploaded_unverified" {
			candidates = append(candidates, upload)
		}
	}
	landed := []string{}
	var mu sync.Mutex
	var wait sync.WaitGroup
	slots := make(chan struct{}, sharedFolderStoreConcurrency)
	for _, upload := range candidates {
		if ctx.Err() != nil {
			break
		}
		wait.Add(1)
		slots <- struct{}{}
		go func(upload db.SharedFolderPendingUpload) {
			defer func() { <-slots; wait.Done() }()
			metadata, err := s.Store.Head(ctx, upload.ObjectKey)
			if err != nil || metadata.ByteSize != upload.ByteSize || metadata.SHA256 != upload.SHA256 {
				return
			}
			mu.Lock()
			landed = append(landed, upload.ID)
			mu.Unlock()
		}(upload)
	}
	wait.Wait()
	return landed
}

// cleanUpSharedFolderObjects deletes objects nothing names any more and
// retires the blobs a folder stopped naming. It is best effort: the change
// already committed, and the Library's orphan sweep collects any object left.
func (s *SpaceLibraryService) cleanUpSharedFolderObjects(ctx context.Context, deleteKeys, retireKeys []string) {
	ctx = context.WithoutCancel(ctx)
	if len(retireKeys) > 0 {
		retired, err := s.database.RetireSharedFolderBlobs(ctx, retireKeys)
		if err != nil {
			log.Printf("shared folder blob retirement: %v", err)
		}
		deleteKeys = append(deleteKeys, retired...)
	}
	var wait sync.WaitGroup
	slots := make(chan struct{}, sharedFolderStoreConcurrency)
	for _, key := range deleteKeys {
		wait.Add(1)
		slots <- struct{}{}
		go func(key string) {
			defer func() { <-slots; wait.Done() }()
			if err := s.Store.Delete(ctx, key); err != nil && !errors.Is(err, ErrLibraryObjectNotFound) {
				log.Printf("shared folder object delete: %v", err)
			}
		}(key)
	}
	wait.Wait()
}
