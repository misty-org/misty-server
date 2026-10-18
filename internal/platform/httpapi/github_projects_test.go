package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/kannachi323/misty/server/internal/integrations/composio"
)

func TestGitHubProjectRepositories(t *testing.T) {
	var sent []string
	var created map[string]any
	call := func(method, endpoint string, body any) (composio.ProxyResult, error) {
		sent = append(sent, method+" "+endpoint)
		switch {
		case method == http.MethodGet:
			return composio.ProxyResult{Status: 200, Data: json.RawMessage(`[{"name":"site","owner":{"login":"ana"},"private":true,
				"default_branch":"trunk","html_url":"https://github.com/ana/site","clone_url":"https://github.com/ana/site.git"}]`)}, nil
		case body.(map[string]any)["name"] == "taken":
			return composio.ProxyResult{Status: 422, Data: json.RawMessage(`{"message":"Repository creation failed.","errors":[{"message":"name already exists on this account"}]}`)}, nil
		default:
			created = body.(map[string]any)
			return composio.ProxyResult{Status: 201, Data: json.RawMessage(`{"name":"bakery","owner":{"login":"ana"},"private":true,"clone_url":"https://github.com/ana/bakery.git"}`)}, nil
		}
	}
	repos, err := githubRepoList(call)
	if err != nil || len(repos) != 1 || repos[0].CloneURL != "https://github.com/ana/site.git" || repos[0].DefaultBranch != "trunk" {
		t.Fatalf("repos = %+v, %v", repos, err)
	}
	repo, err := githubCreateRepo(call, "bakery", true, "Our site")
	if err != nil || repo.CloneURL != "https://github.com/ana/bakery.git" || repo.DefaultBranch != "main" {
		t.Fatalf("created = %+v, %v", repo, err)
	}
	if created["private"] != true || created["auto_init"] != false {
		t.Fatalf("create body = %+v", created)
	}
	var fail *githubFail
	if _, err := githubCreateRepo(call, "taken", true, ""); !errors.As(err, &fail) || fail.message != "GitHub: Repository creation failed.: name already exists on this account" {
		t.Fatalf("taken = %v", err)
	}
	calls := len(sent)
	if _, err := githubCreateRepo(call, "../escape", true, ""); !errors.As(err, &fail) || fail.code != "invalid_repository" || len(sent) != calls {
		t.Fatalf("an invalid name never reaches GitHub: %v", err)
	}
}
