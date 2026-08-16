package api

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type GitHubInstallationInfo struct {
	ID                  int64             `json:"id"`
	Account             GitHubAccount     `json:"account"`
	RepositorySelection string            `json:"repository_selection"`
	Permissions         map[string]string `json:"permissions"`
	Events              []string          `json:"events"`
}
type GitHubAccount struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"`
}

type GitHubRepositoryInfo struct {
	ID            int64           `json:"id"`
	FullName      string          `json:"full_name"`
	DefaultBranch string          `json:"default_branch"`
	CloneURL      string          `json:"clone_url"`
	HTMLURL       string          `json:"html_url"`
	Private       bool            `json:"private"`
	Permissions   map[string]bool `json:"permissions"`
}

type GitHubAppProvider interface {
	Installation(context.Context) (GitHubInstallationInfo, error)
	Repositories(context.Context) ([]GitHubRepositoryInfo, error)
	Snapshot(context.Context, GitHubRepositoryInfo) ([]db.GitHubRepositoryRecord, error)
	Mutate(context.Context, string, GitHubRepositoryInfo, json.RawMessage) (json.RawMessage, error)
	InstallationToken(context.Context) (string, time.Time, error)
}

type GitHubAppProviderFactory func(int64) GitHubAppProvider

type githubAppClient struct {
	installationID int64
	appID, apiBase string
	privateKey     *rsa.PrivateKey
	httpClient     *http.Client
}

func newGitHubAppClient(installationID int64) (*githubAppClient, error) {
	appID := strings.TrimSpace(envconfig.Getenv("GITHUB_APP_ID"))
	privateKey, err := parseGitHubPrivateKey(envconfig.Getenv("GITHUB_APP_PRIVATE_KEY"))
	if appID == "" || err != nil {
		return nil, errors.New("github_app_not_configured")
	}
	apiBase := strings.TrimRight(strings.TrimSpace(envconfig.Getenv("GITHUB_API_BASE_URL")), "/")
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	return &githubAppClient{installationID: installationID, appID: appID, apiBase: apiBase,
		privateKey: privateKey, httpClient: &http.Client{Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func parseGitHubPrivateKey(value string) (*rsa.PrivateKey, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\n`, "\n")
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, errors.New("invalid github private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github private key is not RSA")
	}
	return rsaKey, nil
}

func (c *githubAppClient) appJWT(now time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-30 * time.Second).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": c.appID})
	payload := base64.RawURLEncoding.EncodeToString(claims)
	signing := header + "." + payload
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (c *githubAppClient) request(ctx context.Context, method, path, token string, body any, out any) error {
	var encoded []byte
	if body != nil {
		encoded, _ = json.Marshal(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.apiBase+path, strings.NewReader(string(encoded)))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure map[string]any
		_ = decoder.Decode(&failure)
		return fmt.Errorf("github_api_%d", response.StatusCode)
	}
	if out != nil {
		return decoder.Decode(out)
	}
	return nil
}

func (c *githubAppClient) Installation(ctx context.Context) (GitHubInstallationInfo, error) {
	var out GitHubInstallationInfo
	token, err := c.appJWT(time.Now().UTC())
	if err != nil {
		return out, err
	}
	err = c.request(ctx, http.MethodGet, "/app/installations/"+strconv.FormatInt(c.installationID, 10), token, nil, &out)
	return out, err
}

func (c *githubAppClient) InstallationToken(ctx context.Context) (string, time.Time, error) {
	jwt, err := c.appJWT(time.Now().UTC())
	if err != nil {
		return "", time.Time{}, err
	}
	var response struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	err = c.request(ctx, http.MethodPost, "/app/installations/"+strconv.FormatInt(c.installationID, 10)+"/access_tokens", jwt, map[string]any{}, &response)
	return response.Token, response.ExpiresAt, err
}

func (c *githubAppClient) Repositories(ctx context.Context) ([]GitHubRepositoryInfo, error) {
	token, _, err := c.InstallationToken(ctx)
	if err != nil {
		return nil, err
	}
	repositories := []GitHubRepositoryInfo{}
	for page := 1; page <= 10; page++ {
		var result struct {
			Repositories []GitHubRepositoryInfo `json:"repositories"`
		}
		path := "/installation/repositories?per_page=100&page=" + strconv.Itoa(page)
		if err = c.request(ctx, http.MethodGet, path, token, nil, &result); err != nil {
			return nil, err
		}
		repositories = append(repositories, result.Repositories...)
		if len(result.Repositories) < 100 {
			break
		}
	}
	return repositories, nil
}

func (c *githubAppClient) Snapshot(ctx context.Context, repo GitHubRepositoryInfo) ([]db.GitHubRepositoryRecord, error) {
	token, _, err := c.InstallationToken(ctx)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(repo.FullName, "/")
	if len(parts) != 2 {
		return nil, db.ErrSpaceInvalid
	}
	base := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
	var branches []map[string]any
	var commits []map[string]any
	var issues []map[string]any
	var pulls []map[string]any
	for path, target := range map[string]any{"/branches?per_page=100": &branches, "/commits?per_page=100": &commits, "/issues?state=all&per_page=100": &issues, "/pulls?state=all&per_page=100": &pulls} {
		if err := c.request(ctx, http.MethodGet, base+path, token, nil, target); err != nil {
			return nil, err
		}
	}
	return normalizeGitHubSnapshot(repo, branches, commits, issues, pulls), nil
}

func (c *githubAppClient) Mutate(ctx context.Context, operation string, repo GitHubRepositoryInfo, payload json.RawMessage) (json.RawMessage, error) {
	token, _, err := c.InstallationToken(ctx)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(repo.FullName, "/")
	if len(parts) != 2 {
		return nil, db.ErrSpaceInvalid
	}
	base := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
	path := ""
	var input map[string]any
	if json.Unmarshal(payload, &input) != nil {
		return nil, db.ErrSpaceInvalid
	}
	switch operation {
	case "create_issue":
		path = "/issues"
	case "comment_issue":
		number := TestingFindWorkflowString(input, "number")
		if number == "" {
			return nil, db.ErrSpaceInvalid
		}
		delete(input, "number")
		path = "/issues/" + url.PathEscape(number) + "/comments"
	case "create_branch":
		path = "/git/refs"
	case "create_pull_request":
		path = "/pulls"
	default:
		return nil, db.ErrSpaceInvalid
	}
	var out json.RawMessage
	err = c.request(ctx, http.MethodPost, base+path, token, input, &out)
	return out, err
}
