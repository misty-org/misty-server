package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/kannachi323/misty/server/internal/integrations/composio"
)

// GitHub for projects: listing the person's repositories to clone, and
// creating an empty one to publish a project to, through the GitHub account
// they connected in Integrations. Composio holds the token; the code itself
// moves with git on the person's computer, signed in the way their git is.

var githubNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

type githubCall func(method, endpoint string, body any) (composio.ProxyResult, error)

type githubFail struct {
	status  int
	code    string
	message string
}

func (f *githubFail) Error() string { return f.message }

func githubError(status int, code, message string) error {
	return &githubFail{status: status, code: code, message: message}
}

type githubRepoView struct {
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"defaultBranch"`
	URL           string `json:"url"`
	CloneURL      string `json:"cloneUrl"`
	Description   string `json:"description,omitempty"`
}

type githubRepoBody struct {
	Name          string                 `json:"name"`
	Owner         struct{ Login string } `json:"owner"`
	Private       bool                   `json:"private"`
	DefaultBranch string                 `json:"default_branch"`
	HTMLURL       string                 `json:"html_url"`
	CloneURL      string                 `json:"clone_url"`
	Description   string                 `json:"description"`
}

func (body githubRepoBody) view() githubRepoView {
	branch := body.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	return githubRepoView{Owner: body.Owner.Login, Name: body.Name, Private: body.Private, DefaultBranch: branch,
		URL: body.HTMLURL, CloneURL: body.CloneURL, Description: truncateAgentRuntimeText(body.Description, 200)}
}

// githubMessage turns GitHub's error body into one plain sentence.
func githubMessage(result composio.ProxyResult) string {
	var body struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(result.Data, &body)
	if body.Message == "" {
		return fmt.Sprintf("GitHub answered %d.", result.Status)
	}
	message := body.Message
	for _, detail := range body.Errors {
		if detail.Message != "" {
			message += ": " + detail.Message
			break
		}
	}
	return "GitHub: " + truncateAgentRuntimeText(message, 300)
}

func githubSend(call githubCall, method, endpoint string, body any, ok int) (json.RawMessage, error) {
	result, err := call(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if result.Status == ok {
		return result.Data, nil
	}
	code := "github_rejected"
	switch result.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		code = "github_access"
	case http.StatusUnprocessableEntity:
		code = "github_invalid"
	}
	return nil, githubError(http.StatusBadRequest, code, githubMessage(result))
}

// githubRepoList is the repositories the person owns or works on, most
// recently pushed first.
func githubRepoList(call githubCall) ([]githubRepoView, error) {
	data, err := githubSend(call, http.MethodGet, "/user/repos?per_page=100&sort=pushed&affiliation=owner,collaborator,organization_member", nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var bodies []githubRepoBody
	if json.Unmarshal(data, &bodies) != nil {
		return nil, githubError(http.StatusBadGateway, "github_unreadable", "GitHub's answer couldn't be read.")
	}
	repos := make([]githubRepoView, 0, len(bodies))
	for _, body := range bodies {
		repos = append(repos, body.view())
	}
	return repos, nil
}

// githubCreateRepo makes an empty repository on the person's account, private
// unless they said otherwise. Their computer pushes the project to it.
func githubCreateRepo(call githubCall, name string, private bool, description string) (githubRepoView, error) {
	if !githubNamePattern.MatchString(name) || name == "." || name == ".." {
		return githubRepoView{}, githubError(http.StatusBadRequest, "invalid_repository", "Use letters, numbers, dots, dashes or underscores for the repository name.")
	}
	data, err := githubSend(call, http.MethodPost, "/user/repos", map[string]any{
		"name": name, "private": private, "auto_init": false, "description": truncateAgentRuntimeText(description, 300),
	}, http.StatusCreated)
	if err != nil {
		return githubRepoView{}, err
	}
	var body githubRepoBody
	if json.Unmarshal(data, &body) != nil || body.CloneURL == "" {
		return githubRepoView{}, githubError(http.StatusBadGateway, "github_unreadable", "GitHub's answer couldn't be read.")
	}
	return body.view(), nil
}

// githubAccount is the person's active GitHub connection.
func githubAccount(ctx context.Context, client *composio.Client, user string) (string, error) {
	accounts, err := client.Accounts(ctx, composio.UserID(user), "github")
	if err != nil {
		return "", err
	}
	for _, account := range accounts {
		if account.Active() {
			return account.ID, nil
		}
	}
	return "", githubError(http.StatusConflict, "github_not_connected", "Connect GitHub in Agents › Integrations first.")
}

func writeGitHubError(w http.ResponseWriter, err error) {
	var fail *githubFail
	if errors.As(err, &fail) {
		writeJSON(w, fail.status, map[string]string{"code": fail.code, "message": fail.message})
		return
	}
	writeAppsError(w, err)
}

// GitHubRepos lists the person's GitHub repositories, GET
// /integrations/github/repos, or creates an empty one, POST with
// {name, private, description}.
func (s *SpacesService) GitHubRepos() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		if !requireCoding(w, r, s.database, user, "projects.github") {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		var body struct {
			Name        string `json:"name"`
			Private     *bool  `json:"private"`
			Description string `json:"description"`
		}
		if r.Method == http.MethodPost && decodeJSON(w, r, &body) != nil {
			return
		}
		client, err := composioClient()
		if err != nil {
			writeAppsError(w, err)
			return
		}
		account, err := githubAccount(r.Context(), client, user)
		if err != nil {
			writeGitHubError(w, err)
			return
		}
		call := func(method, endpoint string, payload any) (composio.ProxyResult, error) {
			return client.Proxy(r.Context(), account, method, endpoint, payload)
		}
		if r.Method == http.MethodPost {
			repo, err := githubCreateRepo(call, body.Name, body.Private == nil || *body.Private, body.Description)
			if err != nil {
				writeGitHubError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, map[string]any{"repo": repo})
			return
		}
		repos, err := githubRepoList(call)
		if err != nil {
			writeGitHubError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
	}
}
