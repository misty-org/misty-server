package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// In-use marks over HTTP: a computer marks a drive item it has open (and
// whether it edits it there), renews the mark while the file stays open and
// clears it on close. Before a trash move it can ask the others whether they
// have the item open; they answer by marking it.

// DriveInUseMarks is GET /spaces/{spaceID}/drive/in-use: every unexpired
// in-use mark in the Space, so each computer shows who else has a file open.
// It is fetched again after each drive.in_use event.
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

// MarkDriveInUse is PUT /spaces/{spaceID}/drive/items/{itemID}/in-use {editing}.
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

// ClearDriveInUse is DELETE /spaces/{spaceID}/drive/items/{itemID}/in-use.
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

// CheckDriveInUse is POST /spaces/{spaceID}/drive/items/{itemID}/check-in-use:
// it sends drive.in_use_check and says how long to wait for the answers.
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
