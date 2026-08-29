package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

var homeAppIDs = map[string]bool{
	"home": true, "journal": true, "planner": true, "social": true, "library": true,
	"inbox": true, "browser": true, "code": true, "files": true, "transfers": true,
	"terminal": true, "agents": true, "marketplace": true,
}

func HomeDashboard(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		snapshot, err := database.HomeDashboard(r.Context(), userID, chi.URLParam(r, "spaceID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	}
}

func RecordHomeVisit(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		var body struct {
			Date string `json:"date"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		activityDate, err := time.Parse("2006-01-02", body.Date)
		if err != nil || !homeDateIsCurrent(activityDate, time.Now().UTC()) {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		snapshot, err := database.RecordHomeVisit(r.Context(), userID, chi.URLParam(r, "spaceID"), body.Date)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	}
}

func RecordHomeAppActivity(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		var body struct {
			AppID string `json:"app_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if !homeAppIDs[body.AppID] {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		if err := database.RecordAppActivity(r.Context(), userID, body.AppID); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func homeDateIsCurrent(activityDate, now time.Time) bool {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	delta := activityDate.Sub(today)
	return delta >= -24*time.Hour && delta <= 24*time.Hour
}
