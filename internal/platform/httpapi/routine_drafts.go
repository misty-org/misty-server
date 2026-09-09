package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func RoutineDraftControl(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, database)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		routineID := chi.URLParam(r, "routineID")
		if r.Method == http.MethodPut {
			if envconfig.Getenv("MISTY_ROUTINE_DRAFTS_ENABLED") != "true" {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "routine_drafts_disabled"})
				return
			}
			var body struct {
				ExpectedVersion *int            `json:"expectedVersion"`
				Definition      json.RawMessage `json:"definition"`
			}
			if !decodeCapabilityRequest(w, r, &body) {
				return
			}
			if body.ExpectedVersion == nil {
				writeSDKError(w, cap.ErrInvalid)
				return
			}
			result, err := database.SaveRoutineDraft(r.Context(), userID, routineID, *body.ExpectedVersion, body.Definition)
			if err != nil {
				writeSDKError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, result)
			return
		}
		version := 0
		if raw := r.URL.Query().Get("version"); raw != "" {
			var err error
			version, err = strconv.Atoi(raw)
			if err != nil {
				writeSDKError(w, cap.ErrInvalid)
				return
			}
		}
		result, err := database.RoutineDraft(r.Context(), userID, routineID, version)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}
func RoutineDraftList(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, database)
		if !ok {
			return
		}
		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			var err error
			limit, err = strconv.Atoi(raw)
			if err != nil {
				writeSDKError(w, cap.ErrInvalid)
				return
			}
		}
		page, err := database.RoutineDrafts(r.Context(), userID, r.URL.Query().Get("spaceId"), r.URL.Query().Get("cursor"), limit)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, page)
	}
}
