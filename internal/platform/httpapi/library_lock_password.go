package api

import (
	"context"
	"errors"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"net/http"
)

type libraryPasswordStore interface {
	LibraryPasswordConfigured(context.Context, string) (bool, error)
	SetInitialLibraryPassword(context.Context, string, string) error
}

func LibraryLockPassword(database libraryPasswordStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		userID, ok := authenticatedUser(w, r, nil)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			configured, err := database.LibraryPasswordConfigured(r.Context(), userID)
			if err != nil {
				http.Error(w, "could not check library password", 500)
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"configured": configured})
			return
		}
		var body struct {
			Password     string `json:"password"`
			Confirmation string `json:"confirmation"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if body.Password != body.Confirmation {
			http.Error(w, "library passwords do not match", 400)
			return
		}
		err := database.SetInitialLibraryPassword(r.Context(), userID, body.Password)
		if errors.Is(err, db.ErrLibraryPasswordAlreadySet) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if errors.Is(err, db.ErrLibraryPasswordInvalid) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, "could not set library password", 500)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]bool{"configured": true})
	}
}
