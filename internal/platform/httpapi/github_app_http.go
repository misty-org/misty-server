package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) BeginGitHubAppInstall() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		slug := strings.TrimSpace(envconfig.Getenv("GITHUB_APP_SLUG"))
		if slug == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "github_app_not_configured"})
			return
		}
		var body struct {
			ReturnTo string `json:"return_to"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if !TestingValidProviderReturnPath(body.ReturnTo) {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		state := randomProviderValue(32)
		expires := time.Now().UTC().Add(10 * time.Minute)
		if err := s.database.CreateGitHubAppSetupState(r.Context(), hashProviderValue(state), db.GitHubAppSetupState{
			UserID: userID, SpaceID: chi.URLParam(r, "spaceID"), ReturnTo: body.ReturnTo, ExpiresAt: expires}); err != nil {
			writeSpaceError(w, err)
			return
		}
		installURL := "https://github.com/apps/" + url.PathEscape(slug) + "/installations/new?state=" + url.QueryEscape(state)
		writeJSON(w, http.StatusOK, map[string]any{"provider": "github", "installation_url": installURL, "state_expires_at": expires})
	}
}

func (s *SpacesService) GitHubAppInstallCallback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		installationID, err := strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
		if state == "" || installationID <= 0 || err != nil || r.URL.Query().Get("setup_action") == "request" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_github_setup_callback"})
			return
		}
		stored, err := s.database.ConsumeGitHubAppSetupState(r.Context(), hashProviderValue(state))
		if err != nil {
			writeJSON(w, http.StatusGone, map[string]string{"code": "github_setup_state_expired"})
			return
		}
		provider, err := s.githubAppProvider(installationID)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "github_app_not_configured"})
			return
		}
		info, err := provider.Installation(r.Context())
		if err != nil || info.ID != installationID {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "github_installation_verification_failed"})
			return
		}
		permissions, _ := json.Marshal(info.Permissions)
		events, _ := json.Marshal(info.Events)
		installation, err := s.database.SaveGitHubAppInstallation(r.Context(), stored.UserID, stored.SpaceID, db.GitHubAppInstallation{
			InstallationID: info.ID, AccountID: info.Account.ID, AccountLogin: info.Account.Login, AccountType: info.Account.Type,
			RepositorySelection: info.RepositorySelection, Permissions: permissions, Events: events})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"installation": installation, "return_to": stored.ReturnTo})
	}
}

func (s *SpacesService) GitHubInstallations() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodDelete {
			if err := s.database.DisableGitHubAppInstallation(r.Context(), userID, spaceID, chi.URLParam(r, "installationID")); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		items, err := s.database.GitHubAppInstallations(r.Context(), userID, spaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"installations": items})
	}
}

func (s *SpacesService) GitHubInstallationRepositories() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		installation, err := s.database.GitHubAppInstallation(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "installationID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		provider, err := s.githubAppProvider(installation.InstallationID)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "github_app_not_configured"})
			return
		}
		repos, err := provider.Repositories(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "github_api_error"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"repositories": repos})
	}
}

func githubPayloadHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
