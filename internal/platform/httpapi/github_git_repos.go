package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Repository steps for Git in projects: who the person is on GitHub, their
// repositories, creating and seeding one, branch refs and pull requests.

func githubIdentity(call githubCall) (any, error) {
	data, err := send(call, http.MethodGet, "/user", nil)
	if err != nil {
		return nil, err
	}
	var user struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
		Name  string `json:"name"`
	}
	if json.Unmarshal(data, &user) != nil || user.Login == "" {
		return nil, githubError(http.StatusBadGateway, "github_unreadable", "GitHub's answer couldn't be read.")
	}
	name := strings.TrimSpace(user.Name)
	if name == "" {
		name = user.Login
	}
	// GitHub's no-reply address keeps the person's email private and still
	// links commits to their account.
	return map[string]any{"login": user.Login, "name": name, "email": fmt.Sprintf("%d+%s@users.noreply.github.com", user.ID, user.Login)}, nil
}

type githubRepoView struct {
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"defaultBranch"`
	CanPush       bool   `json:"canPush"`
	URL           string `json:"url"`
	Description   string `json:"description,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}

type githubRepoBody struct {
	Name          string                 `json:"name"`
	Owner         struct{ Login string } `json:"owner"`
	Private       bool                   `json:"private"`
	DefaultBranch string                 `json:"default_branch"`
	HTMLURL       string                 `json:"html_url"`
	Size          int                    `json:"size"`
	Description   string                 `json:"description"`
	PushedAt      string                 `json:"pushed_at"`
	Permissions   struct {
		Push bool `json:"push"`
	} `json:"permissions"`
}

func (body githubRepoBody) view() githubRepoView {
	return githubRepoView{Owner: body.Owner.Login, Name: body.Name, Private: body.Private, DefaultBranch: body.DefaultBranch, CanPush: body.Permissions.Push, URL: body.HTMLURL, Description: truncateAgentRuntimeText(body.Description, 200), UpdatedAt: body.PushedAt}
}

func githubRepos(call githubCall) (any, error) {
	data, err := send(call, http.MethodGet, "/user/repos?per_page=100&sort=pushed&affiliation=owner,collaborator,organization_member", nil)
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
	return map[string]any{"repos": repos}, nil
}

func githubRepo(call githubCall, owner, name string) (any, error) {
	path, err := repoPath(owner, name)
	if err != nil {
		return nil, err
	}
	data, err := send(call, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var body githubRepoBody
	if json.Unmarshal(data, &body) != nil {
		return nil, githubError(http.StatusBadGateway, "github_unreadable", "GitHub's answer couldn't be read.")
	}
	return body.view(), nil
}

// githubCreateRepo makes a repository on the person's account and gives it the
// one-file seed commit GitHub needs before it accepts objects. The first push
// replaces the seed (update_ref with replaceSeed).
func githubCreateRepo(call githubCall, name string, private bool, description string) (any, error) {
	if !validName(name) {
		return nil, githubError(http.StatusBadRequest, "invalid_repository", "Use letters, numbers, dots, dashes or underscores for the repository name.")
	}
	data, err := send(call, http.MethodPost, "/user/repos", map[string]any{
		"name": name, "private": private, "auto_init": false, "description": truncateAgentRuntimeText(description, 300),
	}, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	var body githubRepoBody
	if json.Unmarshal(data, &body) != nil {
		return nil, githubError(http.StatusBadGateway, "github_unreadable", "GitHub's answer couldn't be read.")
	}
	if _, err := githubSeed(call, body.Owner.Login, body.Name); err != nil {
		return nil, err
	}
	view := body.view()
	if view.DefaultBranch == "" {
		view.DefaultBranch = "main"
	}
	return view, nil
}

// githubSeed gives an empty repository the one-file commit GitHub needs
// before it accepts objects; the first push replaces it.
func githubSeed(call githubCall, owner, name string) (any, error) {
	path, err := repoPath(owner, name)
	if err != nil {
		return nil, err
	}
	_, err = send(call, http.MethodPut, path+"/contents/"+githubSeedFile, map[string]any{
		"message": githubSeedMessage, "content": base64.StdEncoding.EncodeToString([]byte("Misty replaces this with the project's first push.\n")),
	}, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	return map[string]any{"seeded": true}, nil
}

func githubRef(call githubCall, owner, name, branch string) (any, error) {
	path, err := repoPath(owner, name)
	if err != nil {
		return nil, err
	}
	if !validBranch(branch) {
		return nil, githubError(http.StatusBadRequest, "invalid_branch", "That isn't a branch name.")
	}
	result, err := call(http.MethodGet, path+"/git/ref/heads/"+branch, nil)
	if err != nil {
		return nil, err
	}
	// 409 means the repository has no commits at all; 404, no such branch.
	if result.Status == http.StatusConflict {
		return map[string]any{"sha": nil, "empty": true}, nil
	}
	if result.Status == http.StatusNotFound {
		return map[string]any{"sha": nil, "empty": false}, nil
	}
	data, err := check(result, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	_ = json.Unmarshal(data, &ref)
	sha, seed := ref.Object.SHA, false
	if sha != "" {
		seed = githubIsSeed(call, path, sha)
	}
	return map[string]any{"sha": sha, "seed": seed, "empty": false}, nil
}

// githubIsSeed reports whether a commit is the seed commit Misty created.
func githubIsSeed(call githubCall, path, sha string) bool {
	data, err := send(call, http.MethodGet, path+"/git/commits/"+sha, nil, http.StatusOK)
	if err != nil {
		return false
	}
	var commit struct {
		Message string               `json:"message"`
		Parents []json.RawMessage    `json:"parents"`
		Tree    struct{ SHA string } `json:"tree"`
	}
	if json.Unmarshal(data, &commit) != nil || strings.TrimSpace(commit.Message) != githubSeedMessage || len(commit.Parents) != 0 {
		return false
	}
	tree, err := send(call, http.MethodGet, path+"/git/trees/"+commit.Tree.SHA, nil, http.StatusOK)
	if err != nil {
		return false
	}
	var listing struct {
		Tree []struct{ Path string } `json:"tree"`
	}
	return json.Unmarshal(tree, &listing) == nil && len(listing.Tree) == 1 && listing.Tree[0].Path == githubSeedFile
}

func githubPullRequest(call githubCall, owner, name, head, base, title, body string) (any, error) {
	path, err := repoPath(owner, name)
	if err != nil {
		return nil, err
	}
	if !validBranch(head) || !validBranch(base) || head == base {
		return nil, githubError(http.StatusBadRequest, "invalid_branch", "A pull request needs two different branches.")
	}
	title = strings.TrimSpace(truncateAgentRuntimeText(title, 250))
	if title == "" {
		return nil, githubError(http.StatusBadRequest, "invalid_title", "A pull request needs a title.")
	}
	result, err := call(http.MethodPost, path+"/pulls", map[string]string{"title": title, "body": truncateAgentRuntimeText(body, 20_000), "head": head, "base": base})
	if err != nil {
		return nil, err
	}
	if result.Status == http.StatusUnprocessableEntity {
		// One may already be open for this branch.
		data, err := send(call, http.MethodGet, path+"/pulls?state=open&head="+url.QueryEscape(owner+":"+head), nil, http.StatusOK)
		if err == nil {
			var open []struct {
				Number int    `json:"number"`
				URL    string `json:"html_url"`
			}
			if json.Unmarshal(data, &open) == nil && len(open) > 0 {
				return map[string]any{"number": open[0].Number, "url": open[0].URL, "existing": true}, nil
			}
		}
		return nil, githubError(http.StatusBadRequest, "github_rejected", githubMessage(result))
	}
	data, err := check(result, nil, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	var created struct {
		Number int    `json:"number"`
		URL    string `json:"html_url"`
	}
	_ = json.Unmarshal(data, &created)
	return map[string]any{"number": created.Number, "url": created.URL}, nil
}
