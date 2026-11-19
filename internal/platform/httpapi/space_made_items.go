package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// Decks (Slides) and workbooks (Data): Space items modelled exactly on drawings. People's edits
// travel over the item's room WebSocket; these routes keep the server's record of each item and
// mint the tickets for its room. Requests made under the wrong Space get the same answer as a
// missing item.

// madeItemKind is the registered shared kind a route serves. Routes are mounted per kind, so an
// unknown name is a programming error that fails every request closed.
func madeItemKind(name string) (db.SpaceItemKind, bool) {
	kind, ok := db.SpaceItemKindNamed(name)
	return kind, ok && kind.Shared
}

// madeItemParam is the route parameter naming one item of a kind: {deckID}, {workbookID}.
func madeItemParam(kind db.SpaceItemKind) string {
	return kind.Name + "ID"
}

// SpaceMadeItems lists and creates one kind's items: GET → {"decks": [...]}, POST {title}.
func (s *SpacesService) SpaceMadeItems(kindName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind, known := madeItemKind(kindName)
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if !known {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			items, err := s.database.AccessibleSpaceMadeItems(r.Context(), kind.Name, userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{kind.Section: items})
		case http.MethodPost:
			var body struct {
				Title string `json:"title"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			item, err := s.database.CreateSpaceMadeItem(r.Context(), kind.Name, userID, spaceID, body.Title)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, item)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// spaceMadeItemInSpace reads one item the caller can view, in the route's Space.
func (s *SpacesService) spaceMadeItemInSpace(r *http.Request, kind db.SpaceItemKind, userID string) (*db.SpaceMadeItem, error) {
	item, err := s.database.SpaceMadeItemByID(r.Context(), kind.Name, userID, chi.URLParam(r, madeItemParam(kind)))
	if err == nil && item.SpaceID != chi.URLParam(r, "spaceID") {
		err = db.ErrSpaceNotFound
	}
	return item, err
}

// SpaceMadeItem reads, renames or deletes one deck or workbook.
func (s *SpacesService) SpaceMadeItem(kindName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind, known := madeItemKind(kindName)
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if !known {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			item, err := s.spaceMadeItemInSpace(r, kind, userID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodPatch:
			var body struct {
				Title string `json:"title"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			current, err := s.spaceMadeItemInSpace(r, kind, userID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			item, err := s.database.RenameSpaceMadeItem(r.Context(), kind.Name, userID, current.ID, body.Title)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodDelete:
			current, err := s.spaceMadeItemInSpace(r, kind, userID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			if err := s.database.DeleteSpaceMadeItem(r.Context(), kind.Name, userID, current.ID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// SpaceMadeItemCollaborationTicket mints a short-lived, single-use credential for the item's
// room after rechecking current membership and the item's state.
func (s *SpacesService) SpaceMadeItemCollaborationTicket(kindName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind, known := madeItemKind(kindName)
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if !known {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		item, err := s.spaceMadeItemInSpace(r, kind, userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		ticket, err := s.JournalCollab.MintItemTicket(kind, userID, item.SpaceID, item.ID, item.Role, item.ACLVersion)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusCreated, ticket)
	}
}
