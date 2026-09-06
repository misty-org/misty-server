package api

import (
	"errors"
	"net/http"

	"github.com/kannachi323/misty/server/internal/appcatalog"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func FinishOnboarding(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		var body struct {
			SpaceName string   `json:"space_name"`
			AppIDs    []string `json:"app_ids"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		// Every account starts with the five Apps that define Misty's default
		// workspace. Additional Apps are acquired later from Discover.
		catalogApps := appcatalog.Defaults()
		installSpecs := make([]db.AppInstallSpec, 0, len(catalogApps))
		for _, item := range catalogApps {
			installSpecs = append(installSpecs, db.AppInstallSpec{
				ID: item.ID, Version: item.Version, PermissionVersion: item.PermissionVersion, Scopes: item.Scopes,
			})
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
