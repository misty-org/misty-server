package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// spaceSearchLimit reads a search's limit: up to 50, 20 when it's left out.
func spaceSearchLimit(r *http.Request) int {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 50 {
		return 20
	}
	return limit
}

// spaceSearchText reads a search's words, and refuses a query that is empty or
// too long to be one.
func spaceSearchText(r *http.Request) (string, bool) {
	text := strings.TrimSpace(r.URL.Query().Get("q"))
	return text, text != "" && len([]rune(text)) <= db.SpaceSearchMaxQuery
}

// Search looks through notes and messages together,
// across the caller's Spaces or the one named by space_id:
// GET /search?q=&space_id=&kinds=note,message&limit=.
// Each result's kind is note|message. Without kinds every
// kind is searched.
func (s *SpacesService) Search() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		text, valid := spaceSearchText(r)
		if !valid {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		kinds, valid := spaceSearchKinds(r.URL.Query().Get("kinds"))
		if !valid {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		hits, err := s.database.SearchSpaceKinds(r.Context(), userID, text, strings.TrimSpace(r.URL.Query().Get("space_id")), kinds, spaceSearchLimit(r))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		results := make([]map[string]any, 0, len(hits))
		for _, hit := range hits {
			results = append(results, map[string]any{
				"kind": hit.Kind, "id": hit.ID, "title": hit.Title, "excerpt": textExcerpt(hit.Text),
				"space_id": hit.SpaceID, "space": hit.SpaceName,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	}
}

// SearchMessages finds the messages the caller can see in one Space.
func (s *SpacesService) SearchMessages() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		text, valid := spaceSearchText(r)
		if !valid {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		messages, err := s.database.SearchSpaceMessages(r.Context(), userID, chi.URLParam(r, "spaceID"), text, spaceSearchLimit(r))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": messages, "count": len(messages)})
	}
}

// spaceSearchKinds reads a search's kinds: "message" and every registered made kind, by its name
// (note) or its section (notes). Empty is
// all of them; an unknown kind refuses the search.
func spaceSearchKinds(raw string) (map[string]bool, bool) {
	kinds := map[string]bool{}
	all := strings.TrimSpace(raw) == ""
	if all {
		kinds["message"] = true
		for _, kind := range db.SpaceItemKinds() {
			kinds[kind.Name] = true
		}
		return kinds, true
	}
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "message" || name == "messages" {
			kinds["message"] = true
			continue
		}
		kind, ok := db.SpaceItemKindNamed(name)
		if !ok {
			return nil, false
		}
		kinds[kind.Name] = true
	}
	return kinds, true
}
