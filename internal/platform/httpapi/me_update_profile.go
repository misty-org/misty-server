package api

import (
	"net/http"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func UpdateProfile(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}

		var body struct {
			Name string `json:"name"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		if body.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}

		if err := database.UpdateUserName(userID, body.Name); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func UpdateDevice(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}

		var body struct {
			Device string `json:"device"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}

		if err := database.UpdateLicenseDevice(userID, body.Device); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func GetSettings(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}

		settings, err := database.GetUserSettingsByID(userID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if settings == nil {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"email_updates_enabled":   settings.EmailUpdatesEnabled,
			"analytics_enabled":       settings.AnalyticsEnabled,
			"error_reporting_enabled": settings.ErrorReportingEnabled,
		})
	}
}

func UpdateSettings(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}

		var body struct {
			EmailUpdatesEnabled   bool `json:"email_updates_enabled"`
			AnalyticsEnabled      bool `json:"analytics_enabled"`
			ErrorReportingEnabled bool `json:"error_reporting_enabled"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}

		if err := database.UpdateUserSettings(userID, db.UserSettings{
			EmailUpdatesEnabled: body.EmailUpdatesEnabled, AnalyticsEnabled: body.AnalyticsEnabled, ErrorReportingEnabled: body.ErrorReportingEnabled,
		}); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func UpdateTelemetryPreferences(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		var body struct {
			AnalyticsEnabled      bool `json:"analytics_enabled"`
			ErrorReportingEnabled bool `json:"error_reporting_enabled"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if err := database.UpdateTelemetryPreferences(userID, body.AnalyticsEnabled, body.ErrorReportingEnabled); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
