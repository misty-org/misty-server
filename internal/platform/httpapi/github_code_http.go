package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) GitHubCodeWorkspaces() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			items, err := s.database.GitHubCodeWorkspaces(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"workspaces": items})
			return
		}
		var body struct {
			InstallationID    string `json:"installation_id"`
			RepositoryID      int64  `json:"repository_id"`
			ClientWorkspaceID string `json:"client_workspace_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		installation, err := s.database.GitHubAppInstallation(r.Context(), userID, spaceID, body.InstallationID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		provider, err := s.githubAppProvider(installation.InstallationID)
		if err != nil {
			writeGitHubProviderError(w, err)
			return
		}
		repositories, err := provider.Repositories(r.Context())
		if err != nil {
			writeGitHubProviderError(w, err)
			return
		}
		var selected *GitHubRepositoryInfo
		for index := range repositories {
			if repositories[index].ID == body.RepositoryID {
				selected = &repositories[index]
				break
			}
		}
		if selected == nil || !githubRepositoryCanRead(*selected) || !githubInstallationCanRead(installation.Permissions) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "github_repository_permission_denied"})
			return
		}
		permissions, _ := json.Marshal(selected.Permissions)
		workspace, err := s.database.CreateGitHubCodeWorkspace(r.Context(), userID, spaceID, body.InstallationID, body.ClientWorkspaceID, db.GitHubRepository{
			ID: selected.ID, FullName: selected.FullName, DefaultBranch: selected.DefaultBranch, CloneURL: selected.CloneURL,
			HTMLURL: selected.HTMLURL, Private: selected.Private, Permissions: permissions})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		count, syncErr := s.syncGitHubCodeWorkspace(r.Context(), workspace, provider, *selected)
		if syncErr != nil {
			_ = s.database.SetGitHubCodeWorkspaceSync(r.Context(), workspace.ID, "", "needs_attention", "github_sync_failed")
		}
		refreshed, _ := s.database.GitHubCodeWorkspace(r.Context(), userID, spaceID, workspace.ID)
		writeJSON(w, http.StatusCreated, map[string]any{"workspace": refreshed, "records_synced": count})
	}
}

func (s *SpacesService) GitHubCodeWorkspace() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, workspaceID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "workspaceID")
		if err := s.database.DisableGitHubCodeWorkspace(r.Context(), userID, spaceID, workspaceID); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) SyncGitHubCodeWorkspace() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		workspace, err := s.database.GitHubCodeWorkspace(r.Context(), userID, spaceID, chi.URLParam(r, "workspaceID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		installation, err := s.database.GitHubAppInstallation(r.Context(), userID, spaceID, workspace.InstallationID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		provider, err := s.githubAppProvider(installation.InstallationID)
		if err != nil {
			writeGitHubProviderError(w, err)
			return
		}
		repo := githubRepositoryFromWorkspace(*workspace)
		count, err := s.syncGitHubCodeWorkspace(r.Context(), workspace, provider, repo)
		if err != nil {
			_ = s.database.SetGitHubCodeWorkspaceSync(r.Context(), workspace.ID, workspace.SyncCursor, "needs_attention", "github_sync_failed")
			writeGitHubProviderError(w, err)
			return
		}
		refreshed, _ := s.database.GitHubCodeWorkspace(r.Context(), userID, spaceID, workspace.ID)
		writeJSON(w, http.StatusOK, map[string]any{"workspace": refreshed, "records_synced": count})
	}
}

func (s *SpacesService) GitHubCodeWorkspaceRecords() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items, err := s.database.GitHubRepositoryRecords(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "workspaceID"), r.URL.Query().Get("record_type"), limit)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"records": items})
	}
}

func (s *SpacesService) GitHubCredentialHandoff() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		handle := randomProviderValue(32)
		expires := time.Now().UTC().Add(90 * time.Second)
		if err := s.database.CreateGitHubCredentialHandoff(r.Context(), hashProviderValue(handle), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "workspaceID"), expires); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, map[string]any{"handoff": handle, "expires_at": expires, "redeem_path": "/native/github/credential-handoffs/redeem"})
	}
}

func (s *SpacesService) RedeemGitHubCredentialHandoff() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var body struct {
			Handoff string `json:"handoff"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		workspace, userID, err := s.database.ConsumeGitHubCredentialHandoffByHandle(r.Context(), hashProviderValue(strings.TrimSpace(body.Handoff)))
		if err != nil {
			writeJSON(w, http.StatusGone, map[string]string{"code": "github_handoff_expired"})
			return
		}
		installation, err := s.database.GitHubAppInstallation(r.Context(), userID, workspace.SpaceID, workspace.InstallationID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		provider, err := s.githubAppProvider(installation.InstallationID)
		if err != nil {
			writeGitHubProviderError(w, err)
			return
		}
		token, expires, err := provider.InstallationToken(r.Context())
		if err != nil {
			writeGitHubProviderError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"clone_url": workspace.CloneURL, "full_name": workspace.FullName, "default_branch": workspace.DefaultBranch, "username": "x-access-token", "token": token, "expires_at": expires})
	}
}

func (s *SpacesService) syncGitHubCodeWorkspace(ctx context.Context, workspace *db.GitHubCodeWorkspace, provider GitHubAppProvider, repo GitHubRepositoryInfo) (int, error) {
	items, err := provider.Snapshot(ctx, repo)
	if err != nil {
		return 0, err
	}
	for index := range items {
		items[index].WorkspaceID = workspace.ID
		if err := s.database.UpsertGitHubRepositoryRecord(ctx, items[index]); err != nil {
			return index, err
		}
	}
	cursor := time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.database.SetGitHubCodeWorkspaceSync(ctx, workspace.ID, cursor, "active", ""); err != nil {
		return len(items), err
	}
	return len(items), nil
}

func githubRepositoryCanRead(repo GitHubRepositoryInfo) bool {
	return repo.Permissions["pull"] || repo.Permissions["push"] || repo.Permissions["admin"] || repo.Permissions["maintain"]
}
func githubInstallationCanRead(raw json.RawMessage) bool {
	var permissions map[string]string
	if json.Unmarshal(raw, &permissions) != nil {
		return false
	}
	return permissions["contents"] == "read" || permissions["contents"] == "write"
}
func githubRepositoryFromWorkspace(item db.GitHubCodeWorkspace) GitHubRepositoryInfo {
	var permissions map[string]bool
	_ = json.Unmarshal(item.Permissions, &permissions)
	return GitHubRepositoryInfo{ID: item.RepositoryID, FullName: item.FullName, DefaultBranch: item.DefaultBranch, CloneURL: item.CloneURL, HTMLURL: item.HTMLURL, Private: item.Private, Permissions: permissions}
}
func writeGitHubProviderError(w http.ResponseWriter, err error) {
	if err != nil && strings.Contains(err.Error(), "not_configured") {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "github_app_not_configured"})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"code": "github_api_error"})
}
