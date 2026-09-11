package api

import (
	"errors"
	"net/http"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func FinishOnboarding(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		var body struct {
			SpaceName      string         `json:"space_name"`
			AppIDs         []string       `json:"app_ids"`
			AppPermissions map[string]int `json:"app_permissions"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		installSpecs, err := reviewedSpaceApps(body.AppIDs, body.AppPermissions)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		completion, err := database.FinishOnboarding(r.Context(), userID, body.SpaceName, installSpecs)
		if err != nil {
			switch {
			case errors.Is(err, db.ErrOnboardingAlreadyComplete):
				writeJSON(w, http.StatusConflict, map[string]string{"code": "onboarding_already_complete"})
			case errors.Is(err, db.ErrSpaceConflict):
				writeJSON(w, http.StatusConflict, map[string]string{"code": "onboarding_request_changed"})
			default:
				writeSpaceError(w, err)
			}
			return
		}
		writeJSON(w, http.StatusCreated, completion)
	}
}
