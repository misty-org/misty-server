package api

import (
	"net/http"
	"strconv"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

func (s *SpaceLibraryService) Groups() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.groupsEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_groups_disabled"})
			return
		}
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			groups, err := s.database.LibraryGroups(r.Context(), userID, spaceID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
		case http.MethodPost:
			var body struct {
				Name  string               `json:"name"`
				Rules db.LibraryGroupRules `json:"rules"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			group, err := s.database.CreateLibraryGroup(r.Context(), userID, spaceID, body.Name, body.Rules)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, group)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpaceLibraryService) GroupItems() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.groupsEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_groups_disabled"})
			return
		}
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		items, err := s.database.LibraryGroupItems(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "groupID"), 200)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func (s *SpaceLibraryService) PeoplePolicy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			policy, err := s.database.LibraryPeoplePolicy(r.Context(), userID, spaceID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, policy)
		case http.MethodPatch:
			var body struct {
				Version               int64 `json:"version"`
				FacesEnabled          bool  `json:"faces_enabled"`
				PetsEnabled           bool  `json:"pets_enabled"`
				AIEnabled             bool  `json:"ai_enabled"`
				SemanticSearchEnabled bool  `json:"semantic_search_enabled"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			if (!s.peopleEnabled && (body.FacesEnabled || body.PetsEnabled)) || (!s.aiEnabled && (body.AIEnabled || body.SemanticSearchEnabled)) {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_intelligence_disabled"})
				return
			}
			policy, err := s.database.UpdateLibraryIntelligencePolicy(r.Context(), userID, spaceID, body.Version, body.FacesEnabled, body.PetsEnabled, body.AIEnabled, body.SemanticSearchEnabled)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, policy)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpaceLibraryService) People() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.peopleEnabled {
			if r.Method == http.MethodGet {
				writeJSON(w, http.StatusOK, map[string]any{"people": []any{}})
				return
			}
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_people_disabled"})
			return
		}
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			people, err := s.database.LibraryPeople(r.Context(), userID, spaceID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"people": people})
		case http.MethodPost:
			var body struct {
				Kind    string   `json:"kind"`
				Name    string   `json:"name"`
				ItemIDs []string `json:"item_ids"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			person, err := s.database.CreateLibraryPerson(r.Context(), userID, spaceID, body.Kind, body.Name, body.ItemIDs)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, person)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpaceLibraryService) Person() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.peopleEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_people_disabled"})
			return
		}
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, personID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "personID")
		switch r.Method {
		case http.MethodGet:
			person, err := s.database.LibraryPerson(r.Context(), userID, spaceID, personID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, person)
		case http.MethodPatch:
			var body struct {
				Version     int64  `json:"version"`
				Name        string `json:"name"`
				CoverItemID string `json:"cover_item_id"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			person, err := s.database.UpdateLibraryPerson(r.Context(), userID, spaceID, personID, body.Version, body.Name, body.CoverItemID)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, person)
		case http.MethodDelete:
			version, _ := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
			if err := s.database.DeleteLibraryPerson(r.Context(), userID, spaceID, personID, version); err != nil {
				writeLibraryError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpaceLibraryService) PersonItems() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.peopleEnabled {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "library_people_disabled"})
			return
		}
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, personID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "personID")
		switch r.Method {
		case http.MethodGet:
			items, err := s.database.LibraryPersonItems(r.Context(), userID, spaceID, personID, 200)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
		case http.MethodPost, http.MethodDelete:
			var body struct {
				ItemIDs []string `json:"item_ids"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			var person *db.LibraryPerson
			var err error
			if r.Method == http.MethodPost {
				person, err = s.database.AddLibraryPersonItems(r.Context(), userID, spaceID, personID, body.ItemIDs)
			} else {
				person, err = s.database.RemoveLibraryPersonItems(r.Context(), userID, spaceID, personID, body.ItemIDs)
			}
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, person)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
