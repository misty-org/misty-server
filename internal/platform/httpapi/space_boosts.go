package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/platform/db"
)

func (s *SpacesService) SpaceBoosts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		state, err := s.database.SpaceBoosts(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	}
}

func (o *OperatorAccounts) space(w http.ResponseWriter, r *http.Request) {
	space, err := o.database.OperatorSpaceByID(r.Context(), chi.URLParam(r, "spaceID"))
	if errors.Is(err, db.ErrSpaceNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "space_not_found"})
		return
	}
	if err != nil {
		writeOperatorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"space": space})
}

func (o *OperatorAccounts) setSpaceBoosts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Boosts *int64 `json:"boosts"`
	}
	if decodeJSON(w, r, &body) != nil {
		return
	}
	if body.Boosts == nil {
		writeOperatorError(w, operatorInvalid("boosts is required: a whole number from 0 to 1000"))
		return
	}
	spaceID := chi.URLParam(r, "spaceID")
	err := o.database.SetSpaceBoosts(r.Context(), spaceID, chi.URLParam(r, "id"), *body.Boosts)
	switch {
	case errors.Is(err, db.ErrSpaceNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "space_not_found"})
		return
	case errors.Is(err, db.ErrSpaceInvalid):
		writeOperatorError(w, operatorInvalid("boosts is a whole number from 0 to 1000"))
		return
	case err != nil:
		writeOperatorError(w, err)
		return
	}
	o.space(w, r)
}
