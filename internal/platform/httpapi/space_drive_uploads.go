package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// Drive uploads over HTTP. The app asks with a file's digest and size; the
// answer is that the Space already has the content, or where to PUT it: one
// presigned URL up to 100 MiB, else a presigned URL per part of a multipart
// upload. Asking again for the same content resumes the same upload with
// fresh URLs. The complete call checks the object in R2 (after joining the
// parts of a multipart upload), and the item's create or change names the
// upload to take its bytes in.

type driveUploadTarget struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
}

type driveUploadPartTarget struct {
	Number int32  `json:"number"`
	URL    string `json:"url"`
}

type driveMultipartTarget struct {
	PartSize int64                   `json:"part_size"`
	Parts    []driveUploadPartTarget `json:"parts"`
}

// drivePartSize is a multipart upload's part size for a file: 64 MiB, or
// larger in whole MiB so the file fits in 10,000 parts.
func drivePartSize(size int64) int64 {
	part := driveMinPartBytes
	if needed := (size + driveMaxParts - 1) / driveMaxParts; needed > part {
		part = (needed + (1<<20 - 1)) / (1 << 20) * (1 << 20)
	}
	return part
}

// StartDriveUpload is POST /spaces/{spaceID}/drive/uploads {sha256, size}.
func (s *SpaceDriveService) StartDriveUpload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if _, ok := driveDevice(w, r); !ok {
			return
		}
		var body struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if s.Presigner == nil {
			writeDriveError(w, ErrLibraryDirectTransferUnavailable)
			return
		}
		if body.Size > driveSingleUploadMaxBytes && s.Multipart == nil {
			writeDriveError(w, errLibraryMultipartUnavailable)
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		sha := strings.ToLower(strings.TrimSpace(body.SHA256))
		started, err := s.database.StartDriveUpload(r.Context(), userID, spaceID, sha, body.Size, time.Now().Add(driveUploadLifetime).UTC())
		if err != nil {
			writeDriveError(w, err)
			return
		}
		s.abortDriveUploads(r.Context(), started.Released)
		upload := started.Upload
		if started.Present {
			writeJSON(w, http.StatusOK, map[string]any{"upload_id": upload.ID, "present": true})
			return
		}
		if upload.Size <= driveSingleUploadMaxBytes {
			signed, err := s.Presigner.PresignPut(r.Context(), upload.ObjectKey, LibraryObjectMetadata{
				ByteSize: upload.Size, SHA256: upload.SHA256, MIMEType: db.DriveMIMEType,
			}, s.UploadURLTTL)
			if err != nil {
				writeDriveError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"upload_id": upload.ID,
				"single": driveUploadTarget{URL: signed.URL, Method: signed.Method, Headers: signed.Headers}})
			return
		}
		target, err := s.multipartTarget(r.Context(), userID, spaceID, upload)
		if err != nil {
			writeDriveError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"upload_id": upload.ID, "multipart": target})
	}
}

// multipartTarget starts the upload's multipart upload unless it has one,
// then signs every part.
func (s *SpaceDriveService) multipartTarget(ctx context.Context, userID, spaceID string, upload db.DriveUpload) (*driveMultipartTarget, error) {
	if upload.MultipartUploadID == "" {
		partSize := drivePartSize(upload.Size)
		multipartID, err := s.Multipart.CreateMultipartUpload(ctx, upload.ObjectKey, LibraryObjectMetadata{
			ByteSize: upload.Size, SHA256: upload.SHA256, MIMEType: db.DriveMIMEType,
		})
		if err != nil {
			return nil, err
		}
		if err := s.database.SetDriveUploadMultipart(ctx, userID, spaceID, upload.ID, multipartID, partSize); err != nil {
			// Another request of the caller's started one first; that one stays.
			_ = s.Multipart.AbortMultipartUpload(context.WithoutCancel(ctx), upload.ObjectKey, multipartID)
			if !errors.Is(err, db.ErrLibraryConflict) {
				return nil, err
			}
			current, err := s.database.DriveUploadByID(ctx, userID, spaceID, upload.ID)
			if err != nil {
				return nil, err
			}
			if current.MultipartUploadID == "" {
				return nil, db.ErrDriveUploadIncomplete
			}
			upload = *current
		} else {
			upload.MultipartUploadID, upload.PartSize = multipartID, partSize
		}
	}
	target := &driveMultipartTarget{PartSize: upload.PartSize, Parts: []driveUploadPartTarget{}}
	for number, offset := int32(1), int64(0); offset < upload.Size; number, offset = number+1, offset+upload.PartSize {
		url, err := s.Multipart.PresignUploadPart(ctx, upload.ObjectKey, upload.MultipartUploadID, number, min(upload.PartSize, upload.Size-offset), drivePartURLTTL)
		if err != nil {
			return nil, err
		}
		target.Parts = append(target.Parts, driveUploadPartTarget{Number: number, URL: url})
	}
	return target, nil
}

// verifyDriveUpload checks a pending upload's object in R2 against what was
// reserved and records that it landed. A mismatched object is given up.
// Missing bytes answer ErrDriveUploadIncomplete.
func (s *SpaceDriveService) verifyDriveUpload(ctx context.Context, userID, spaceID string, upload db.DriveUpload) error {
	metadata, err := s.Store.Head(ctx, upload.ObjectKey)
	if errors.Is(err, ErrLibraryObjectNotFound) {
		return db.ErrDriveUploadIncomplete
	}
	if err != nil {
		return err
	}
	if metadata.ByteSize != upload.Size || metadata.SHA256 != upload.SHA256 {
		log.Printf("drive upload %s failed verification", upload.ID)
		if rejectErr := s.database.RejectDriveUpload(ctx, userID, spaceID, upload.ID); rejectErr != nil {
			return rejectErr
		}
		s.cleanUpDriveObjects(ctx, []string{upload.ObjectKey}, nil)
		return db.ErrLibraryUploadMismatch
	}
	return s.database.MarkDriveUploadLanded(ctx, userID, spaceID, upload.ID)
}

// landDriveUpload checks a single-PUT upload a create or change names, in
// case the app sent it straight on without the complete call. Whatever it
// finds, the database decides whether the upload can be used.
func (s *SpaceDriveService) landDriveUpload(r *http.Request, userID, spaceID, uploadID string) {
	if uploadID == "" {
		return
	}
	upload, err := s.database.DriveUploadByID(r.Context(), userID, spaceID, uploadID)
	if err != nil || upload.MultipartUploadID != "" || (upload.State != "initiated" && upload.State != "uploading") {
		return
	}
	_ = s.verifyDriveUpload(r.Context(), userID, spaceID, *upload)
}

// CompleteDriveUpload is POST /spaces/{spaceID}/drive/uploads/{uploadID}/complete
// with {parts} for a multipart upload; a single upload is checked by HEAD.
func (s *SpaceDriveService) CompleteDriveUpload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if _, ok := driveDevice(w, r); !ok {
			return
		}
		var body struct {
			Parts []LibraryUploadPart `json:"parts"`
		}
		if decodeOptionalJSON(w, r, &body, maxDriveCompleteBodyBytes) != nil {
			return
		}
		spaceID, uploadID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "uploadID")
		upload, err := s.database.DriveUploadByID(r.Context(), userID, spaceID, uploadID)
		if err != nil {
			writeDriveError(w, err)
			return
		}
		switch {
		case upload.State == "uploaded_unverified" || upload.State == "ready":
			writeJSON(w, http.StatusOK, map[string]string{"upload_id": upload.ID})
			return
		case !upload.Pending():
			writeDriveError(w, db.ErrDriveUploadIncomplete)
			return
		}
		if upload.MultipartUploadID != "" {
			if len(body.Parts) == 0 || s.Multipart == nil {
				writeDriveError(w, db.ErrLibraryInvalid)
				return
			}
			for _, part := range body.Parts {
				if part.Number < 1 || part.Number > driveMaxParts || strings.TrimSpace(part.ETag) == "" {
					writeDriveError(w, db.ErrLibraryInvalid)
					return
				}
			}
			if err := s.Multipart.CompleteMultipartUpload(r.Context(), upload.ObjectKey, upload.MultipartUploadID, body.Parts); err != nil {
				// A completion whose answer was lost already joined the parts, and
				// R2 no longer knows the multipart upload: the object is there to check.
				if _, headErr := s.Store.Head(r.Context(), upload.ObjectKey); headErr != nil {
					log.Printf("drive multipart completion for %s: %v", upload.ID, err)
					writeDriveError(w, db.ErrDriveUploadIncomplete)
					return
				}
			}
		} else if len(body.Parts) > 0 {
			writeDriveError(w, db.ErrLibraryInvalid)
			return
		}
		if err := s.verifyDriveUpload(r.Context(), userID, spaceID, *upload); err != nil {
			if errors.Is(err, db.ErrLibraryUploadMismatch) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "upload_verification_failed"})
				return
			}
			writeDriveError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"upload_id": upload.ID})
	}
}
