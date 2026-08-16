package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type fakeFigmaProvider struct {
	fileGets        atomic.Int32
	commentPosts    atomic.Int32
	webhookDeletes  atomic.Int32
	lastCommentFile string

	mu        sync.Mutex
	passcodes map[string]string
	prefix    string
}

func (f *fakeFigmaProvider) Projects(context.Context, string) ([]FigmaProject, error) {
	return []FigmaProject{{ID: "project-1", Name: "Launch project"}}, nil
}

func (f *fakeFigmaProvider) ProjectFiles(context.Context, string) ([]FigmaFileSummary, error) {
	return []FigmaFileSummary{{Key: "allowed-file", Name: "Allowed file", LastModified: "2026-08-19T12:00:00Z"}}, nil
}

func (f *fakeFigmaProvider) File(_ context.Context, key string) (FigmaFileContext, error) {
	f.fileGets.Add(1)
	return FigmaFileContext{
		Key: key, Name: "Launch canvas", Version: "version-1",
		LastModified: "2026-08-19T12:00:00Z", EditorType: "figma",
		ThumbnailURL: "https://figma.test/thumbnail.png",
		Document:     json.RawMessage(`{"id":"0:0","name":"Document","type":"DOCUMENT","children":[]}`),
	}, nil
}

func (f *fakeFigmaProvider) Versions(context.Context, string) ([]FigmaVersion, error) {
	return []FigmaVersion{{ID: "version-1", CreatedAt: "2026-08-19T12:00:00Z", Label: "Launch"}}, nil
}

func (f *fakeFigmaProvider) Comments(context.Context, string) ([]FigmaComment, error) {
	return []FigmaComment{}, nil
}

func (f *fakeFigmaProvider) PostComment(_ context.Context, fileKey, message, nodeID string) (FigmaComment, error) {
	f.commentPosts.Add(1)
	f.lastCommentFile = fileKey
	return FigmaComment{
		ID: "comment-1", Message: message, CreatedAt: "2026-08-19T12:01:00Z",
		User:       map[string]any{"id": "user-1", "handle": "Misty"},
		ClientMeta: map[string]any{"node_id": nodeID},
	}, nil
}

func (f *fakeFigmaProvider) CreateWebhook(_ context.Context, eventType, _, _, _, passcode string) (FigmaWebhook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.passcodes == nil {
		f.passcodes = map[string]string{}
	}
	f.passcodes[eventType] = passcode
	return FigmaWebhook{ID: f.prefix + "-webhook-" + strings.ToLower(eventType), EventType: eventType, Status: "ACTIVE"}, nil
}

func (f *fakeFigmaProvider) DeleteWebhook(context.Context, string) error {
	f.webhookDeletes.Add(1)
	return nil
}

func (f *fakeFigmaProvider) webhook(eventType string) (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prefix + "-webhook-" + strings.ToLower(eventType), f.passcodes[eventType]
}

type figmaHTTPFixture struct {
	database   *db.Database
	service    *SpacesService
	provider   *fakeFigmaProvider
	owner      *db.User
	space      *db.Space
	connection *db.ConnectedAccount
	token      string
	router     *chi.Mux
}

func setupFigmaHTTP(t *testing.T, capabilities ...string) figmaHTTPFixture {
	t.Helper()
	database := openPresenceTestDatabase(t)
	suffix := strings.ReplaceAll(uuid.NewString()[:12], "-", "")
	owner, err := database.CreateUserWithUsername("Figma Owner", "figma_"+suffix, uniqueTestEmail("figma-http"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(t.Context(), owner.ID, "Figma HTTP")
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("f", 32)))
	service, err := NewSpacesService(database, nil, key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := service.TestingEncryptConnectedAccountAccessToken("figma", "figma-access-token")
	if err != nil {
		t.Fatal(err)
	}
	connection, err := database.SaveConnectedAccount(t.Context(), db.ConnectedAccount{
		UserID: owner.ID, Provider: "figma", AccountID: "figma-account-1",
		AccountDisplay: "designer@example.com", CredentialCiphertext: ciphertext,
		CredentialNonce: nonce, KeyVersion: 1, Capabilities: capabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeFigmaProvider{prefix: suffix}
	service.TestingSetFigmaProviderFactory(func(token string) FigmaProvider {
		if token != "figma-access-token" {
			t.Fatalf("Figma provider received token %q", token)
		}
		return provider
	})
	router := chi.NewRouter()
	router.MethodFunc(http.MethodPost, "/spaces/{spaceID}/drawings/figma/bindings", service.FigmaBindings())
	router.Post("/spaces/{spaceID}/drawings/figma/bindings/{bindingID}/comments", service.FigmaComments())
	router.Post("/spaces/{spaceID}/drawings/figma/bindings/{bindingID}/reconcile-webhooks", service.ReconcileFigmaWebhooks())
	router.Delete("/connections/{connectionID}", service.DeleteConnectedAccount())
	router.Post("/provider-callbacks/figma", service.FigmaWebhook())
	return figmaHTTPFixture{
		database: database, service: service, provider: provider, owner: owner, space: space,
		connection: connection, token: newConversationTestBearerToken(t, database, owner.ID), router: router,
	}
}

func bindFigmaResource(t *testing.T, fixture figmaHTTPFixture, body map[string]any) db.FigmaBinding {
	t.Helper()
	body["connection_id"] = fixture.connection.ID
	recorder := performConversationRequest(t, fixture.router, http.MethodPost,
		"/spaces/"+fixture.space.ID+"/drawings/figma/bindings", fixture.token, body)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("bind status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "figma-access-token") {
		t.Fatalf("binding response exposed access token: %s", recorder.Body.String())
	}
	var response struct {
		Binding db.FigmaBinding `json:"binding"`
		Records int             `json:"records_synced"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Binding.ID == "" || response.Records < 1 {
		t.Fatalf("binding response=%#v", response)
	}
	return response.Binding
}

func TestFigmaDirectFileBindingNormalizesURLAndRejectsInsecureSources(t *testing.T) {
	fixture := setupFigmaHTTP(t, "drawings_read")
	binding := bindFigmaResource(t, fixture, map[string]any{
		"resource_type": "file", "file_url": "https://www.figma.com/design/file-key/Launch",
	})
	if binding.FileKey != "file-key" || binding.ExternalID != "file-key" || fixture.provider.fileGets.Load() < 2 {
		t.Fatalf("binding=%#v file gets=%d", binding, fixture.provider.fileGets.Load())
	}

	rejected := performConversationRequest(t, fixture.router, http.MethodPost,
		"/spaces/"+fixture.space.ID+"/drawings/figma/bindings", fixture.token,
		map[string]any{"connection_id": fixture.connection.ID, "resource_type": "file", "file_url": "http://figma.com/design/insecure-key/Launch"})
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("insecure Figma URL status=%d body=%s", rejected.Code, rejected.Body.String())
	}
}

func TestFigmaCommentRequiresConfirmationIdempotencyAndProjectMembership(t *testing.T) {
	fixture := setupFigmaHTTP(t, "drawings_read", "drawings_comments")
	fileBinding := bindFigmaResource(t, fixture, map[string]any{"resource_type": "file", "file_key": "file-key"})
	path := "/spaces/" + fixture.space.ID + "/drawings/figma/bindings/" + fileBinding.ID + "/comments"

	unconfirmed := performConversationRequest(t, fixture.router, http.MethodPost, path, fixture.token,
		map[string]any{"message": "Review this", "confirmed": false})
	if unconfirmed.Code != http.StatusConflict || fixture.provider.commentPosts.Load() != 0 ||
		!strings.Contains(unconfirmed.Body.String(), "figma_comment_confirmation_required") {
		t.Fatalf("unconfirmed status=%d body=%s posts=%d", unconfirmed.Code, unconfirmed.Body.String(), fixture.provider.commentPosts.Load())
	}
	missingKey := performConversationRequest(t, fixture.router, http.MethodPost, path, fixture.token,
		map[string]any{"message": "Review this", "confirmed": true})
	if missingKey.Code != http.StatusBadRequest || fixture.provider.commentPosts.Load() != 0 {
		t.Fatalf("missing idempotency status=%d body=%s posts=%d", missingKey.Code, missingKey.Body.String(), fixture.provider.commentPosts.Load())
	}
	body := map[string]any{"message": "Review this", "node_id": "1:2", "confirmed": true, "idempotency_key": "comment-action-1"}
	created := performConversationRequest(t, fixture.router, http.MethodPost, path, fixture.token, body)
	if created.Code != http.StatusCreated || fixture.provider.commentPosts.Load() != 1 || fixture.provider.lastCommentFile != "file-key" {
		t.Fatalf("created status=%d body=%s posts=%d file=%q", created.Code, created.Body.String(), fixture.provider.commentPosts.Load(), fixture.provider.lastCommentFile)
	}
	replay := performConversationRequest(t, fixture.router, http.MethodPost, path, fixture.token, body)
	if replay.Code != http.StatusConflict || fixture.provider.commentPosts.Load() != 1 ||
		!strings.Contains(replay.Body.String(), "figma_comment_already_claimed") {
		t.Fatalf("replay status=%d body=%s posts=%d", replay.Code, replay.Body.String(), fixture.provider.commentPosts.Load())
	}
	records, err := fixture.database.FigmaContentRecords(t.Context(), fixture.owner.ID, fixture.space.ID, fileBinding.ID, "comment", "", 20)
	var provenance map[string]any
	if len(records) == 1 {
		_ = json.Unmarshal(records[0].Provenance, &provenance)
	}
	if err != nil || len(records) != 1 || provenance["source"] != "user" {
		t.Fatalf("comment records=%#v err=%v", records, err)
	}

	projectBinding := bindFigmaResource(t, fixture, map[string]any{"resource_type": "project", "project_id": "project-1"})
	projectPath := "/spaces/" + fixture.space.ID + "/drawings/figma/bindings/" + projectBinding.ID + "/comments"
	outside := performConversationRequest(t, fixture.router, http.MethodPost, projectPath, fixture.token,
		map[string]any{"file_key": "outside-file", "message": "Not shared", "confirmed": true, "idempotency_key": "outside-action"})
	if outside.Code != http.StatusForbidden || fixture.provider.commentPosts.Load() != 1 {
		t.Fatalf("outside project file status=%d body=%s posts=%d", outside.Code, outside.Body.String(), fixture.provider.commentPosts.Load())
	}
}

func TestFigmaWebhookValidatesPasscodeEventResourceAndDuplicates(t *testing.T) {
	fixture := setupFigmaHTTP(t, "drawings_read", "drawings_webhooks")
	bindFigmaResource(t, fixture, map[string]any{"resource_type": "file", "file_key": "file-key"})
	webhookID, passcode := fixture.provider.webhook("FILE_UPDATE")
	if passcode == "" {
		t.Fatal("binding did not create FILE_UPDATE webhook")
	}
	post := func(payload map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(payload)
		request := httptest.NewRequest(http.MethodPost, "/provider-callbacks/figma", strings.NewReader(string(raw)))
		response := httptest.NewRecorder()
		fixture.router.ServeHTTP(response, request)
		return response
	}
	base := map[string]any{"webhook_id": webhookID, "event_type": "FILE_UPDATE", "passcode": passcode, "file_key": "file-key", "timestamp": "2026-08-19T12:02:00Z"}

	badPasscode := cloneFigmaPayload(base)
	badPasscode["passcode"] = "wrong"
	if response := post(badPasscode); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_figma_passcode") {
		t.Fatalf("bad passcode status=%d body=%s", response.Code, response.Body.String())
	}
	wrongEvent := cloneFigmaPayload(base)
	wrongEvent["event_type"] = "FILE_COMMENT"
	if response := post(wrongEvent); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "figma_webhook_event_mismatch") {
		t.Fatalf("event mismatch status=%d body=%s", response.Code, response.Body.String())
	}
	wrongFile := cloneFigmaPayload(base)
	wrongFile["file_key"] = "outside-file"
	if response := post(wrongFile); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "figma_webhook_resource_mismatch") {
		t.Fatalf("resource mismatch status=%d body=%s", response.Code, response.Body.String())
	}
	accepted := post(base)
	if accepted.Code != http.StatusOK || !strings.Contains(accepted.Body.String(), `"refresh_required":true`) {
		t.Fatalf("accepted status=%d body=%s", accepted.Code, accepted.Body.String())
	}
	duplicate := post(base)
	if duplicate.Code != http.StatusOK || !strings.Contains(duplicate.Body.String(), `"duplicate":true`) {
		t.Fatalf("duplicate status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}

	deleted := cloneFigmaPayload(base)
	deleted["event_type"] = "FILE_DELETE"
	deleted["timestamp"] = "2026-08-19T12:03:00Z"
	if response := post(deleted); response.Code != http.StatusOK {
		t.Fatalf("FILE_DELETE for FILE_UPDATE subscription status=%d body=%s", response.Code, response.Body.String())
	}
}

func cloneFigmaPayload(source map[string]any) map[string]any {
	copy := make(map[string]any, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func TestFigmaRateLimitPreservesRetryAfterWithoutLeakingBearer(t *testing.T) {
	var authorization string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Retry-After", "37")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"slow down"}`))
	}))
	defer provider.Close()
	t.Setenv("FIGMA_API_BASE_URL", provider.URL)

	fixture := setupFigmaHTTP(t, "drawings_projects")
	// Use the real provider client for this transport/error mapping contract.
	fixture.service.TestingSetFigmaProviderFactory(nil)
	router := chi.NewRouter()
	router.Get("/figma/teams/{teamID}/projects", fixture.service.FigmaProjects())
	recorder := performConversationRequest(t, router, http.MethodGet,
		"/figma/teams/team-1/projects?connection_id="+fixture.connection.ID, fixture.token, nil)
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "37" {
		t.Fatalf("rate limit status=%d retry-after=%q body=%s", recorder.Code, recorder.Header().Get("Retry-After"), recorder.Body.String())
	}
	if authorization != "Bearer figma-access-token" || strings.Contains(recorder.Body.String(), "figma-access-token") {
		t.Fatalf("authorization=%q response=%s", authorization, recorder.Body.String())
	}
}

func TestFigmaWebhookReconcileAndDisconnectLifecycle(t *testing.T) {
	fixture := setupFigmaHTTP(t, "drawings_read", "drawings_webhooks")
	binding := bindFigmaResource(t, fixture, map[string]any{"resource_type": "file", "file_key": "lifecycle-file"})
	path := "/spaces/" + fixture.space.ID + "/drawings/figma/bindings/" + binding.ID + "/reconcile-webhooks"
	reconciled := performConversationRequest(t, fixture.router, http.MethodPost, path, fixture.token, nil)
	if reconciled.Code != http.StatusOK || !strings.Contains(reconciled.Body.String(), `"subscriptions"`) {
		t.Fatalf("reconcile status=%d body=%s", reconciled.Code, reconciled.Body.String())
	}
	disconnected := performConversationRequest(t, fixture.router, http.MethodDelete, "/connections/"+fixture.connection.ID, fixture.token, nil)
	if disconnected.Code != http.StatusNoContent {
		t.Fatalf("disconnect status=%d body=%s", disconnected.Code, disconnected.Body.String())
	}
	bindings, err := fixture.database.FigmaBindings(t.Context(), fixture.owner.ID, fixture.space.ID)
	if err != nil || len(bindings) != 0 || fixture.provider.webhookDeletes.Load() != 3 {
		t.Fatalf("bindings=%#v deletes=%d err=%v", bindings, fixture.provider.webhookDeletes.Load(), err)
	}
}

var _ FigmaProvider = (*fakeFigmaProvider)(nil)
