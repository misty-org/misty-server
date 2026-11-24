package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

type libraryRoute func(w http.ResponseWriter, r *http.Request, userID, spaceID string)

func (s *SpaceLibraryService) inSpace(next libraryRoute) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		direct, err := s.database.IsDirectSpace(r.Context(), spaceID)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		if direct {
			writeLibraryError(w, db.ErrLibraryNotFound)
			return
		}
		next(w, r, userID, spaceID)
	}
}

func (s *SpaceLibraryService) inDM(next libraryRoute) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		dmID := chi.URLParam(r, "dmID")
		if err := s.database.RequireDirectMember(r.Context(), userID, dmID); err != nil {
			writeLibraryError(w, err)
			return
		}
		next(w, r, userID, dmID)
	}
}

func (s *SpaceLibraryService) InitiateUpload() http.HandlerFunc { return s.inSpace(s.initiateUpload) }
func (s *SpaceLibraryService) FinalizeUpload() http.HandlerFunc { return s.inSpace(s.finalizeUpload) }
func (s *SpaceLibraryService) AbortUpload() http.HandlerFunc    { return s.inSpace(s.abortUpload) }

func (s *SpaceLibraryService) DownloadAttachment() http.HandlerFunc {
	return s.inSpace(func(w http.ResponseWriter, r *http.Request, userID, spaceID string) {
		download, err := s.database.MessageAttachmentDownload(r.Context(), userID, spaceID, chi.URLParam(r, "attachmentID"))
		s.downloadAttachment(w, r, download, err)
	})
}

func (s *SpaceLibraryService) DMInitiateUpload() http.HandlerFunc { return s.inDM(s.initiateUpload) }
func (s *SpaceLibraryService) DMFinalizeUpload() http.HandlerFunc { return s.inDM(s.finalizeUpload) }
func (s *SpaceLibraryService) DMAbortUpload() http.HandlerFunc    { return s.inDM(s.abortUpload) }

func (s *SpaceLibraryService) DMDownloadAttachment() http.HandlerFunc {
	return s.inDM(func(w http.ResponseWriter, r *http.Request, userID, dmID string) {
		download, err := s.database.DirectAttachmentDownload(r.Context(), userID, dmID, chi.URLParam(r, "attachmentID"))
		s.downloadAttachment(w, r, download, err)
	})
}
