package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) GitHubCodeWorkspaceActions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, workspaceID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "workspaceID")
		var body struct {
			Operation string          `json:"operation"`
			Payload   json.RawMessage `json:"payload"`
			Confirmed bool            `json:"confirmed"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.RequireSpacePermission(r.Context(), userID, spaceID, db.PermissionIntegrationsManage); err != nil {
			writeSpaceError(w, err)
			return
		}
		workspace, err := s.database.GitHubCodeWorkspace(r.Context(), userID, spaceID, workspaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !body.Confirmed {
			_ = s.database.RecordGitHubMutationAudit(r.Context(), userID, spaceID, workspaceID, "user", body.Operation, githubMutationTarget(body.Payload), "github_mutation_confirmation_required", false, false)
			writeJSON(w, http.StatusConflict, map[string]string{"code": "github_mutation_confirmation_required"})
			return
		}
		installation, err := s.database.GitHubAppInstallation(r.Context(), userID, spaceID, workspace.InstallationID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if err := githubValidateMutation(body.Operation, body.Payload, installation.Permissions, workspace.Permissions); err != nil {
			_ = s.database.RecordGitHubMutationAudit(r.Context(), userID, spaceID, workspaceID, "user", body.Operation, githubMutationTarget(body.Payload), "permission_denied", true, false)
			if errors.Is(err, db.ErrSpaceInvalid) {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "github_write_permission_denied"})
			return
		}
		provider, err := s.githubAppProvider(installation.InstallationID)
		if err != nil {
			writeGitHubProviderError(w, err)
			return
		}
		result, err := provider.Mutate(r.Context(), body.Operation, githubRepositoryFromWorkspace(*workspace), body.Payload)
		if err != nil {
			_ = s.database.RecordGitHubMutationAudit(r.Context(), userID, spaceID, workspaceID, "user", body.Operation, githubMutationTarget(body.Payload), "github_api_error", true, false)
			writeGitHubProviderError(w, err)
			return
		}
		_ = s.database.RecordGitHubMutationAudit(r.Context(), userID, spaceID, workspaceID, "user", body.Operation, githubMutationTarget(body.Payload), "", true, true)
		writeJSON(w, http.StatusOK, map[string]any{"operation": body.Operation, "result": result})
	}
}

func githubValidateMutation(operation string, payload, installationPermissions, repositoryPermissions json.RawMessage) error {
	var input map[string]any
	var app map[string]string
	var repo map[string]bool
	if json.Unmarshal(payload, &input) != nil || json.Unmarshal(installationPermissions, &app) != nil || json.Unmarshal(repositoryPermissions, &repo) != nil {
		return db.ErrSpaceInvalid
	}
	if !(repo["push"] || repo["admin"] || repo["maintain"]) {
		return db.ErrSpaceForbidden
	}
	required := ""
	fields := []string{}
	switch operation {
	case "create_issue":
		required = "issues"
		fields = []string{"title"}
	case "comment_issue":
		required = "issues"
		fields = []string{"number", "body"}
	case "create_branch":
		required = "contents"
		fields = []string{"ref", "sha"}
	case "create_pull_request":
		required = "pull_requests"
		fields = []string{"title", "head", "base"}
	default:
		return db.ErrSpaceInvalid
	}
	if app[required] != "write" {
		return db.ErrSpaceForbidden
	}
	for _, field := range fields {
		if TestingFindWorkflowString(input, field) == "" {
			return db.ErrSpaceInvalid
		}
	}
	return nil
}

func githubMutationTarget(payload json.RawMessage) string {
	var input map[string]any
	_ = json.Unmarshal(payload, &input)
	return githubFirstNonEmpty(TestingFindWorkflowString(input, "number"), TestingFindWorkflowString(input, "ref"), TestingFindWorkflowString(input, "head"))
}
func githubFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
