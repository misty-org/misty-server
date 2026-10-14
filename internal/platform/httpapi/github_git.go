package api

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/kannachi323/misty/server/internal/integrations/composio"
)

// Git for projects (docs/plans/agent-git-projects.md, route B): the desktop
// moves commits to and from GitHub through GitHub's API, via the GitHub
// account the person connected through Composio. Composio holds the token;
// it never reaches Misty or the computer. Every object GitHub creates or
// returns is checked against the hash the desktop expects, so history stays
// identical on both sides. This relays one request at a time and keeps nothing.

var (
	githubNamePattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
	githubBranchPattern = regexp.MustCompile(`^[A-Za-z0-9_./-]{1,120}$`)
	gitSHAPattern       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	gitModePattern      = regexp.MustCompile(`^(040000|40000|100644|100755|120000|160000)$`)
)

const (
	// Composio returns at most 4 MB per answer; base64 makes files a third larger.
	githubMaxFetchBlob = 2_900_000
	// What one request may carry to GitHub.
	githubMaxPushBytes = 24 << 20
	githubMaxBatch     = 60
	// The commit GitHub needs before an empty repository accepts objects.
	githubSeedMessage = "Created by Misty"
	githubSeedFile    = ".misty-seed"
)

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

func validName(value string) bool {
	return githubNamePattern.MatchString(value) && value != "." && value != ".."
}

func validBranch(value string) bool {
	return githubBranchPattern.MatchString(value) && !strings.Contains(value, "..") && !strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "-")
}

func repoPath(owner, name string) (string, error) {
	if !validName(owner) || !validName(name) {
		return "", githubError(http.StatusBadRequest, "invalid_repository", "That isn't a GitHub repository name.")
	}
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name), nil
}

func gitObjectSHA(kind string, content []byte) string {
	hash := sha1.New()
	fmt.Fprintf(hash, "%s %d\x00", kind, len(content))
	hash.Write(content)
	return hex.EncodeToString(hash.Sum(nil))
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
		return fmt.Sprintf("GitHub answered %d", result.Status)
	}
	message := body.Message
	// "Validation Failed" says little; GitHub's details say why.
	for _, detail := range body.Errors {
		if detail.Message != "" {
			message += ": " + detail.Message
			break
		}
	}
	return "GitHub: " + truncateAgentRuntimeText(message, 300)
}

// send makes one GitHub request and returns its body when the status is one
// of ok (200 when none is given).
func send(call githubCall, method, endpoint string, body any, ok ...int) (json.RawMessage, error) {
	result, err := call(method, endpoint, body)
	return check(result, err, ok...)
}

func check(result composio.ProxyResult, err error, ok ...int) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	if len(ok) == 0 {
		ok = []int{http.StatusOK}
	}
	for _, status := range ok {
		if result.Status == status {
			return result.Data, nil
		}
	}
	code := "github_rejected"
	switch result.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		code = "github_access"
	case http.StatusNotFound:
		code = "github_not_found"
	}
	return nil, githubError(http.StatusBadRequest, code, githubMessage(result))
}

// GitHubGit runs one Git step against the person's GitHub repository.
func (s *SpacesService) GitHubGit() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		var request struct {
			Op   string          `json:"op"`
			Args json.RawMessage `json:"args"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, githubMaxPushBytes+(1<<20))).Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request", "message": "That request is too large or malformed."})
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
		call := func(method, endpoint string, body any) (composio.ProxyResult, error) {
			return client.Proxy(r.Context(), account, method, endpoint, body)
		}
		out, err := githubOperation(call, request.Op, request.Args)
		if err != nil {
			writeGitHubError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func writeGitHubError(w http.ResponseWriter, err error) {
	var fail *githubFail
	if errors.As(err, &fail) {
		writeJSON(w, fail.status, map[string]string{"code": fail.code, "message": fail.message})
		return
	}
	writeAppsError(w, err)
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

func githubOperation(call githubCall, op string, raw json.RawMessage) (any, error) {
	var args struct {
		Owner       string            `json:"owner"`
		Name        string            `json:"name"`
		Branch      string            `json:"branch"`
		SHA         string            `json:"sha"`
		SHAs        []string          `json:"shas"`
		Private     *bool             `json:"private"`
		Description string            `json:"description"`
		Create      bool              `json:"create"`
		ReplaceSeed bool              `json:"replaceSeed"`
		Head        string            `json:"head"`
		Base        string            `json:"base"`
		Title       string            `json:"title"`
		Body        string            `json:"body"`
		Blobs       []json.RawMessage `json:"blobs"`
		Trees       []json.RawMessage `json:"trees"`
		Commits     []json.RawMessage `json:"commits"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, githubError(http.StatusBadRequest, "invalid_request", "Those Git arguments are malformed.")
		}
	}
	switch op {
	case "identity":
		return githubIdentity(call)
	case "repos":
		return githubRepos(call)
	case "repo":
		return githubRepo(call, args.Owner, args.Name)
	case "create_repo":
		private := args.Private == nil || *args.Private
		return githubCreateRepo(call, args.Name, private, args.Description)
	case "ref":
		return githubRef(call, args.Owner, args.Name, args.Branch)
	case "seed":
		return githubSeed(call, args.Owner, args.Name)
	case "push_blobs":
		return githubPushBlobs(call, args.Owner, args.Name, args.Blobs)
	case "push_trees":
		return githubPushTrees(call, args.Owner, args.Name, args.Trees)
	case "push_commits":
		return githubPushCommits(call, args.Owner, args.Name, args.Commits)
	case "update_ref":
		return githubUpdateRef(call, args.Owner, args.Name, args.Branch, args.SHA, args.Create, args.ReplaceSeed)
	case "commits":
		return githubCommits(call, args.Owner, args.Name, args.SHAs)
	case "tree":
		return githubTree(call, args.Owner, args.Name, args.SHA)
	case "blobs":
		return githubBlobs(call, args.Owner, args.Name, args.SHAs)
	case "pull_request":
		return githubPullRequest(call, args.Owner, args.Name, args.Head, args.Base, args.Title, args.Body)
	}
	return nil, githubError(http.StatusBadRequest, "invalid_operation", "Misty doesn't know that Git step.")
}
