package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// libraryImageACLVersion is the access version an image room's tickets carry. Library items
// have no per-item access list to revoke: membership and visibility are rechecked each time a
// ticket is minted, and tickets live for a minute.
const libraryImageACLVersion = 1

// SpaceLibraryImageCollaborationTicket opens a library image's edit room, where the people
// editing it (and their agents) share the edit live. The image itself stays in the library;
// the room holds only the edit until someone saves.
func (s *SpacesService) SpaceLibraryImageCollaborationTicket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, itemID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID")
		role, err := s.database.LibraryImageCollaborationRole(r.Context(), userID, spaceID, itemID)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		ticket, err := s.JournalCollab.MintImageTicket(userID, spaceID, itemID, role, libraryImageACLVersion)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusCreated, ticket)
	}
}
