package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *SpaceDriveService) DriveInUseMarks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		marks, err := s.database.DriveInUseMarks(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeDriveError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusOK, map[string]any{"marks": marks})
	}
}

func (s *SpaceDriveService) MarkDriveInUse() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		device, ok := driveDevice(w, r)
		if !ok {
			return
		}
		var body struct {
			Editing bool `json:"editing"`
		}
		if decodeOptionalJSON(w, r, &body, maxJSONBodyBytes) != nil {
			return
		}
		lease, err := s.database.MarkDriveInUse(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID"), device, body.Editing)
		if err != nil {
			writeDriveError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, lease)
	}
}

func (s *SpaceDriveService) ClearDriveInUse() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		device, ok := driveDevice(w, r)
		if !ok {
			return
		}
		if err := s.database.ClearDriveInUse(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID"), device); err != nil {
			writeDriveError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpaceDriveService) CheckDriveInUse() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if _, ok := driveDevice(w, r); !ok {
			return
		}
		if err := s.database.AskDriveInUse(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID")); err != nil {
			writeDriveError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"wait_ms": driveInUseCheckWait})
	}
}
