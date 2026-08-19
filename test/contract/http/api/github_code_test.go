package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type fakeGitHubAppProvider struct{ mutations atomic.Int32 }

func (f *fakeGitHubAppProvider) Installation(context.Context) (GitHubInstallationInfo, error) {
	return GitHubInstallationInfo{ID: 101, Account: GitHubAccount{ID: 7, Login: "misty", Type: "Organization"}, RepositorySelection: "selected", Permissions: map[string]string{"contents": "write", "issues": "write", "pull_requests": "write"}, Events: []string{"push", "issues", "pull_request"}}, nil
}
func (f *fakeGitHubAppProvider) Repositories(context.Context) ([]GitHubRepositoryInfo, error) {
	return []GitHubRepositoryInfo{{ID: 202, FullName: "misty/app", DefaultBranch: "main", CloneURL: "https://github.com/misty/app.git", HTMLURL: "https://github.com/misty/app", Private: true, Permissions: map[string]bool{"pull": true, "push": true}}}, nil
}
func (f *fakeGitHubAppProvider) Snapshot(context.Context, GitHubRepositoryInfo) ([]db.GitHubRepositoryRecord, error) {
	return []db.GitHubRepositoryRecord{{RepositoryID: 202, RecordType: "branch", ExternalID: "main", RefName: "main", SHA: "abc", Title: "main", Fingerprint: strings.Repeat("a", 64), Provenance: json.RawMessage(`{"source":"test"}`)}}, nil
}
func (f *fakeGitHubAppProvider) Mutate(context.Context, string, GitHubRepositoryInfo, json.RawMessage) (json.RawMessage, error) {
	f.mutations.Add(1)
	return json.RawMessage(`{"number":9}`), nil
}
func (f *fakeGitHubAppProvider) InstallationToken(context.Context) (string, time.Time, error) {
	return "short-lived-installation-token", time.Now().Add(time.Hour), nil
}

func setupGitHubHTTP(t *testing.T) (*db.Database, *SpacesService, *fakeGitHubAppProvider, *db.User, *db.Space, *db.GitHubCodeWorkspace) {
	database := openPresenceTestDatabase(t)
	owner, err := database.CreateUserWithUsername("GitHub HTTP", "github_"+strings.ReplaceAll(uuid.NewString()[:12], "-", ""), uniqueTestEmail("github-http"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(t.Context(), owner.ID, "GitHub HTTP")
	if err != nil {
		t.Fatal(err)
	}
	permissions := json.RawMessage(`{"contents":"write","issues":"write","pull_requests":"write"}`)
	installation, err := database.SaveGitHubAppInstallation(t.Context(), owner.ID, space.ID, db.GitHubAppInstallation{InstallationID: 101, AccountID: 7, AccountLogin: "misty", AccountType: "Organization", RepositorySelection: "selected", Permissions: permissions, Events: json.RawMessage(`[]`)})
	if err != nil {
		t.Fatal(err)
	}
	repoPermissions := json.RawMessage(`{"pull":true,"push":true}`)
	workspace, err := database.CreateGitHubCodeWorkspace(t.Context(), owner.ID, space.ID, installation.ID, "native-workspace-123", db.GitHubRepository{ID: 202, FullName: "misty/app", DefaultBranch: "main", CloneURL: "https://github.com/misty/app.git", HTMLURL: "https://github.com/misty/app", Private: true, Permissions: repoPermissions})
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("g", 32)))
	spaces, err := NewSpacesService(database, nil, key)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeGitHubAppProvider{}
	spaces.TestingSetGitHubAppProviderFactory(func(int64) GitHubAppProvider { return fake })
	return database, spaces, fake, owner, space, workspace
}

func TestGitHubActionsRequireConfirmationAndNeverExposeToken(t *testing.T) {
	database, spaces, fake, owner, space, workspace := setupGitHubHTTP(t)
	router := chi.NewRouter()
	router.Post("/spaces/{spaceID}/code/github/workspaces/{workspaceID}/actions", spaces.GitHubCodeWorkspaceActions())
	router.Post("/spaces/{spaceID}/code/github/workspaces/{workspaceID}/credential-handoff", spaces.GitHubCredentialHandoff())
	router.Post("/native/github/credential-handoffs/redeem", spaces.RedeemGitHubCredentialHandoff())
	token := newConversationTestBearerToken(t, database, owner.ID)
	base := "/spaces/" + space.ID + "/code/github/workspaces/" + workspace.ID
	rejected := performConversationRequest(t, router, http.MethodPost, base+"/actions", token, map[string]any{"operation": "create_pull_request", "confirmed": false, "payload": map[string]any{"title": "Ship", "head": "feature", "base": "main"}})
	if rejected.Code != http.StatusConflict || fake.mutations.Load() != 0 {
		t.Fatalf("rejected=%d body=%s mutations=%d", rejected.Code, rejected.Body.String(), fake.mutations.Load())
	}
	var rejectedAudits int
	if err := database.Conn.QueryRow(`SELECT COUNT(*) FROM github_mutation_audit WHERE workspace_id=$1 AND confirmed=FALSE AND success=FALSE AND error_code='github_mutation_confirmation_required'`, workspace.ID).Scan(&rejectedAudits); err != nil || rejectedAudits != 1 {
		t.Fatalf("rejected audit count=%d err=%v", rejectedAudits, err)
	}
	approved := performConversationRequest(t, router, http.MethodPost, base+"/actions", token, map[string]any{"operation": "create_pull_request", "confirmed": true, "payload": map[string]any{"title": "Ship", "head": "feature", "base": "main"}})
	if approved.Code != http.StatusOK || fake.mutations.Load() != 1 || strings.Contains(approved.Body.String(), "installation-token") {
		t.Fatalf("approved=%d body=%s mutations=%d", approved.Code, approved.Body.String(), fake.mutations.Load())
	}
	created := performConversationRequest(t, router, http.MethodPost, base+"/credential-handoff", token, nil)
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), "installation-token") {
		t.Fatalf("handoff=%d body=%s", created.Code, created.Body.String())
	}
	var handoff struct {
		Handoff string `json:"handoff"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &handoff)
	redeemed := performConversationRequest(t, router, http.MethodPost, "/native/github/credential-handoffs/redeem", "", map[string]any{"handoff": handoff.Handoff})
	if redeemed.Code != http.StatusOK || !strings.Contains(redeemed.Body.String(), "short-lived-installation-token") || redeemed.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("redeem=%d headers=%v body=%s", redeemed.Code, redeemed.Header(), redeemed.Body.String())
	}
	replay := performConversationRequest(t, router, http.MethodPost, "/native/github/credential-handoffs/redeem", "", map[string]any{"handoff": handoff.Handoff})
	if replay.Code != http.StatusGone {
		t.Fatalf("replay=%d body=%s", replay.Code, replay.Body.String())
	}
}

func TestGitHubAppInstallCallbackAndRepositoryDiscovery(t *testing.T) {
	database := openPresenceTestDatabase(t)
	suffix := strings.ReplaceAll(uuid.NewString()[:12], "-", "")
	owner, err := database.CreateUserWithUsername("GitHub Install", "ghi_"+suffix, uniqueTestEmail("github-install"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(t.Context(), owner.ID, "GitHub Install")
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("i", 32)))
	spaces, err := NewSpacesService(database, nil, key)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeGitHubAppProvider{}
	spaces.TestingSetGitHubAppProviderFactory(func(int64) GitHubAppProvider { return fake })
	t.Setenv("GITHUB_APP_SLUG", "misty-test")
	router := chi.NewRouter()
	router.Post("/spaces/{spaceID}/integrations/github/install", spaces.BeginGitHubAppInstall())
	router.Get("/oauth/github/app/callback", spaces.GitHubAppInstallCallback())
	router.Get("/spaces/{spaceID}/integrations/github/installations", spaces.GitHubInstallations())
	router.Get("/spaces/{spaceID}/integrations/github/installations/{installationID}/repositories", spaces.GitHubInstallationRepositories())
	token := newConversationTestBearerToken(t, database, owner.ID)
	started := performConversationRequest(t, router, http.MethodPost, "/spaces/"+space.ID+"/integrations/github/install", token, map[string]any{"return_to": "/spaces/" + space.ID + "/code"})
	if started.Code != http.StatusOK {
		t.Fatalf("start=%d body=%s", started.Code, started.Body.String())
	}
	var start struct {
		InstallationURL string `json:"installation_url"`
	}
	_ = json.Unmarshal(started.Body.Bytes(), &start)
	parsed, _ := url.Parse(start.InstallationURL)
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatalf("missing state: %s", started.Body.String())
	}
	callback := performConversationRequest(t, router, http.MethodGet, "/oauth/github/app/callback?state="+url.QueryEscape(state)+"&installation_id=101&setup_action=install", "", nil)
	if callback.Code != http.StatusOK || strings.Contains(callback.Body.String(), "token") {
		t.Fatalf("callback=%d body=%s", callback.Code, callback.Body.String())
	}
	var response struct {
		Installation db.GitHubAppInstallation `json:"installation"`
	}
	_ = json.Unmarshal(callback.Body.Bytes(), &response)
	if response.Installation.ID == "" || response.Installation.InstallationID != 101 {
		t.Fatalf("installation=%#v", response.Installation)
	}
	repositories := performConversationRequest(t, router, http.MethodGet, "/spaces/"+space.ID+"/integrations/github/installations/"+response.Installation.ID+"/repositories", token, nil)
	if repositories.Code != http.StatusOK || !strings.Contains(repositories.Body.String(), "misty/app") || strings.Contains(repositories.Body.String(), "installation-token") {
		t.Fatalf("repos=%d body=%s", repositories.Code, repositories.Body.String())
	}
}

func TestGitHubSignedWebhookIsIdempotent(t *testing.T) {
	database, spaces, _, owner, space, workspace := setupGitHubHTTP(t)
	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-test-secret")
	router := chi.NewRouter()
	router.Post("/provider-callbacks/github", spaces.GitHubWebhook())
	payload := []byte(`{"ref":"refs/heads/main","after":"def","installation":{"id":101},"repository":{"id":202},"commits":[{"id":"def","message":"webhook commit","url":"https://github.test/def","timestamp":"2026-08-19T12:00:00Z","author":{"username":"mika"}}]}`)
	mac := hmac.New(sha256.New, []byte("webhook-test-secret"))
	_, _ = mac.Write(payload)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	deliveryID := "delivery-" + uuid.NewString()
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/provider-callbacks/github", strings.NewReader(string(payload)))
		request.Header.Set("X-GitHub-Delivery", deliveryID)
		request.Header.Set("X-GitHub-Event", "push")
		request.Header.Set("X-Hub-Signature-256", signature)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if attempt == 0 && response.Code != http.StatusAccepted {
			t.Fatalf("first=%d body=%s", response.Code, response.Body.String())
		}
		if attempt == 1 && response.Code != http.StatusOK {
			t.Fatalf("duplicate=%d body=%s", response.Code, response.Body.String())
		}
	}
	records, err := database.GitHubRepositoryRecords(t.Context(), owner.ID, space.ID, workspace.ID, "commit", 10)
	if err != nil || len(records) != 1 || records[0].SHA != "def" {
		t.Fatalf("records=%#v err=%v", records, err)
	}
}

func TestGitHubInstallationLifecycleAndRepositoryRemovalWebhooks(t *testing.T) {
	database, spaces, _, owner, space, _ := setupGitHubHTTP(t)
	secret := "lifecycle-secret"
	t.Setenv("GITHUB_WEBHOOK_SECRET", secret)
	router := chi.NewRouter()
	router.Post("/provider-callbacks/github", spaces.GitHubWebhook())
	send := func(event string, payload string) {
		t.Helper()
		raw := []byte(payload)
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(raw)
		request := httptest.NewRequest(http.MethodPost, "/provider-callbacks/github", strings.NewReader(payload))
		request.Header.Set("X-GitHub-Delivery", "delivery-"+uuid.NewString())
		request.Header.Set("X-GitHub-Event", event)
		request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("%s status=%d body=%s", event, response.Code, response.Body.String())
		}
	}
	send("installation", `{"action":"suspended","installation":{"id":101}}`)
	items, err := database.GitHubAppInstallations(t.Context(), owner.ID, space.ID)
	if err != nil || len(items) != 1 || items[0].Status != "suspended" {
		t.Fatalf("suspended installations=%#v err=%v", items, err)
	}
	send("installation", `{"action":"unsuspended","installation":{"id":101}}`)
	items, err = database.GitHubAppInstallations(t.Context(), owner.ID, space.ID)
	if err != nil || items[0].Status != "active" {
		t.Fatalf("unsuspended installations=%#v err=%v", items, err)
	}
	send("installation_repositories", `{"action":"removed","installation":{"id":101},"repositories_removed":[{"id":202,"full_name":"misty/app"}]}`)
	workspaces, err := database.GitHubCodeWorkspaces(t.Context(), owner.ID, space.ID)
	if err != nil || len(workspaces) != 0 {
		t.Fatalf("removed workspaces=%#v err=%v", workspaces, err)
	}
	send("installation", `{"action":"deleted","installation":{"id":101}}`)
	items, err = database.GitHubAppInstallations(t.Context(), owner.ID, space.ID)
	if err != nil || len(items) != 0 {
		t.Fatalf("deleted installations=%#v err=%v", items, err)
	}
}
