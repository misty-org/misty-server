package app

import (
	"net/http"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func (s *Server) mountGitHubCodeRoutes(prefix string, spaces *api.SpacesService) {
	s.Router.Post(prefix+"/spaces/{spaceID}/integrations/github/install", spaces.BeginGitHubAppInstall())
	s.Router.Get(prefix+"/oauth/github/app/callback", spaces.GitHubAppInstallCallback())
	s.Router.Get(prefix+"/spaces/{spaceID}/integrations/github/installations", spaces.GitHubInstallations())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/integrations/github/installations/{installationID}", spaces.GitHubInstallations())
	s.Router.Get(prefix+"/spaces/{spaceID}/integrations/github/installations/{installationID}/repositories", spaces.GitHubInstallationRepositories())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/code/github/workspaces", spaces.GitHubCodeWorkspaces())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/code/github/workspaces", spaces.GitHubCodeWorkspaces())
	s.Router.Delete(prefix+"/spaces/{spaceID}/code/github/workspaces/{workspaceID}", spaces.GitHubCodeWorkspace())
	s.Router.Post(prefix+"/spaces/{spaceID}/code/github/workspaces/{workspaceID}/sync", spaces.SyncGitHubCodeWorkspace())
	s.Router.Get(prefix+"/spaces/{spaceID}/code/github/workspaces/{workspaceID}/records", spaces.GitHubCodeWorkspaceRecords())
	s.Router.Post(prefix+"/spaces/{spaceID}/code/github/workspaces/{workspaceID}/actions", spaces.GitHubCodeWorkspaceActions())
	s.Router.Post(prefix+"/spaces/{spaceID}/code/github/workspaces/{workspaceID}/credential-handoff", spaces.GitHubCredentialHandoff())
	s.Router.Post(prefix+"/native/github/credential-handoffs/redeem", spaces.RedeemGitHubCredentialHandoff())
	s.Router.Post(prefix+"/provider-callbacks/github", spaces.GitHubWebhook())
}
