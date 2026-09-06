package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/appcatalog"
	mailintegration "github.com/kannachi323/misty/server/internal/integrations/mail"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

type fakeMailProvider struct {
	activeGets   atomic.Int32
	maxGets      atomic.Int32
	sends        atomic.Int32
	lastChanges  mailintegration.ThreadChanges
	lastDraft    mailintegration.DraftInput
	accountError error
	folderError  error
	threadError  error
}

func TestMailRPCPreservesOpaqueProviderIDsAndLargeDraftAttachments(t *testing.T) {
	database := openPresenceTestDatabase(t)
	owner, err := database.CreateUserWithUsername("Mail RPC", "mail_"+uuid.NewString()[:8], uniqueTestEmail("mail-rpc"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(t.Context(), owner.ID, "Inbox")
	if err != nil {
		t.Fatal(err)
	}
	app, ok := appcatalog.Find("inbox")
	if !ok {
		t.Fatal("missing Inbox")
	}
	if _, err := database.InstallUserApp(t.Context(), owner.ID, app.ID, app.Version, app.PermissionVersion, app.Scopes); err != nil {
		t.Fatal(err)
	}
	token := "mail-rpc-" + uuid.NewString()
	if _, err := database.CreateAppRuntimeSession(t.Context(), owner.ID, app.ID, security.HashToken(token), space.ID, db.AppRuntimeSessionTTL); err != nil {
		t.Fatal(err)
	}
	spaces, err := NewSpacesService(database, nil, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("m", 32))))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := spaces.TestingEncryptConnectedAccountAccessToken("google", "test-only-provider-token")
	if err != nil {
		t.Fatal(err)
	}
	connection, err := database.SaveConnectedAccount(t.Context(), db.ConnectedAccount{UserID: owner.ID, Provider: "google", AccountID: "account-1", AccountDisplay: "owner@example.invalid", CredentialCiphertext: ciphertext, CredentialNonce: nonce, KeyVersion: 1, Capabilities: []string{"mail"}, GrantedScopes: []string{"gmail.modify", "gmail.send"}})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeMailProvider{}
	spaces.TestingSetMailProviderFactory(func(account db.ConnectedAccount, credential string) (mailintegration.Provider, error) {
		if account.ID != connection.ID || credential != "test-only-provider-token" {
			t.Fatal("incorrect private credential binding")
		}
		return fake, nil
	})
	router := mailTestRouter(spaces)
	router.Post("/app-runtime/rpc", OfficialAppRPC(database, router, ""))
	call := func(method string, params map[string]any) map[string]any {
		t.Helper()
		response := performConversationRequest(t, router, "POST", "/app-runtime/rpc", token, map[string]any{"protocol": 2, "method": method, "params": params})
		if response.Code < 200 || response.Code >= 300 {
			t.Fatalf("%s status=%d body=%s", method, response.Code, response.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, id := range []string{"a+b/c==", "%2F", "%25?x#y", "a/../b"} {
		thread := call("mail.threads.get", map[string]any{"path": map[string]string{"threadID": id}, "query": map[string]string{"connection_id": connection.ID}})
		if thread["thread"].(map[string]any)["provider_id"] != id {
			t.Fatalf("corrupted RPC thread ID %q", id)
		}
		action := call("mail.threads.action", map[string]any{"path": map[string]string{"threadID": id}, "body": map[string]any{"connection_id": connection.ID, "read": true}})
		if action["thread_id"] != id {
			t.Fatalf("corrupted action ID %q", id)
		}
		draft := call("mail.drafts.update", map[string]any{"path": map[string]string{"draftID": id}, "body": map[string]any{"connection_id": connection.ID, "to": []any{}, "subject": "Test", "text": "Draft only"}})
		if draft["draft"].(map[string]any)["provider_id"] != id {
			t.Fatalf("corrupted draft ID %q", id)
		}
		// Raw REST requests must also decode only once, including a literal %2F.
		response := performConversationRequest(t, router, "GET", "/mail/threads/"+url.PathEscape(id)+"?connection_id="+url.QueryEscape(connection.ID), token, nil)
		if response.Code != 200 {
			t.Fatalf("direct read failed: %d %s", response.Code, response.Body.String())
		}
		var direct map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &direct); err != nil {
			t.Fatal(err)
		}
		if direct["thread"].(map[string]any)["provider_id"] != id {
			t.Fatalf("corrupted direct ID %q", id)
		}
	}
	attachment := strings.Repeat("x", 4<<20)
	call("mail.drafts.create", map[string]any{"body": map[string]any{"connection_id": connection.ID, "to": []any{}, "subject": "Draft attachment", "text": "Test only", "attachments": []map[string]any{{"filename": "test.txt", "content_type": "text/plain", "data": base64.StdEncoding.EncodeToString([]byte(attachment)), "inline": false}}}})
	if len(fake.lastDraft.Attachments) != 1 || string(fake.lastDraft.Attachments[0].Data) != attachment {
		t.Fatal("large attachment did not reach provider intact")
	}
	if fake.sends.Load() != 0 {
		t.Fatal("draft checks must not send mail")
	}
}

func testMailMessage() mailintegration.Message {
	return mailintegration.Message{Provenance: mailintegration.Provenance{Provider: "gmail", ProviderID: "message-1", AccountID: "account-1"},
		ThreadID: "thread-1", RFC822ID: "rfc-1", Subject: "Launch", From: mailintegration.Address{Name: "A", Email: "a@example.com"},
		To: []mailintegration.Address{{Name: "B", Email: "b@example.com"}}, SentAt: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
		Snippet: "Ready", Body: mailintegration.Body{Text: "Ready", HadHTML: true}, Labels: []string{"INBOX"}, Unread: true,
		Attachments: []mailintegration.Attachment{{Provenance: mailintegration.Provenance{Provider: "gmail", ProviderID: "attachment-1", AccountID: "account-1"}, MessageID: "message-1", Filename: "brief.pdf", ContentType: "application/pdf", Size: 12}}}
}

func (f *fakeMailProvider) Account(context.Context) (mailintegration.Account, error) {
	if f.accountError != nil {
		return mailintegration.Account{}, f.accountError
	}
	return mailintegration.Account{Provenance: mailintegration.Provenance{Provider: "gmail", ProviderID: "owner@example.com", AccountID: "account-1"}, Email: "owner@example.com", DisplayName: "Owner", Total: 20, Unread: 3}, nil
}
func (f *fakeMailProvider) ListFolders(context.Context) ([]mailintegration.Folder, error) {
	if f.folderError != nil {
		return nil, f.folderError
	}
	return []mailintegration.Folder{{Provenance: mailintegration.Provenance{Provider: "gmail", ProviderID: "INBOX", AccountID: "account-1"}, Name: "Inbox", Kind: mailintegration.FolderInbox, System: true, Total: 20, Unread: 3}}, nil
}
func (f *fakeMailProvider) ListThreads(context.Context, mailintegration.ListThreadsRequest) (mailintegration.ThreadPage, error) {
	if f.threadError != nil {
		return mailintegration.ThreadPage{}, f.threadError
	}
	threads := make([]mailintegration.Thread, 8)
	for index := range threads {
		threads[index].Provenance = mailintegration.Provenance{Provider: "gmail", ProviderID: "thread-" + string(rune('1'+index)), AccountID: "account-1"}
	}
	return mailintegration.ThreadPage{Threads: threads, NextPageToken: "next", EstimatedTotal: 80}, nil
}
func (f *fakeMailProvider) GetThread(ctx context.Context, id string) (mailintegration.Thread, error) {
	current := f.activeGets.Add(1)
	defer f.activeGets.Add(-1)
	for {
		maximum := f.maxGets.Load()
		if current <= maximum || f.maxGets.CompareAndSwap(maximum, current) {
			break
		}
	}
	select {
	case <-ctx.Done():
		return mailintegration.Thread{}, ctx.Err()
	case <-time.After(3 * time.Millisecond):
	}
	message := testMailMessage()
	message.ThreadID = id
	return mailintegration.Thread{Provenance: mailintegration.Provenance{Provider: "gmail", ProviderID: id, AccountID: "account-1"}, Subject: "Launch", Snippet: "Ready", Participants: []mailintegration.Address{{Email: "a@example.com"}}, Messages: []mailintegration.Message{message}, Labels: []string{"INBOX"}, LastMessageAt: message.SentAt, Unread: true}, nil
}
func (f *fakeMailProvider) ModifyThread(_ context.Context, id string, changes mailintegration.ThreadChanges) (mailintegration.ThreadChangeResult, error) {
	f.lastChanges = changes
	return mailintegration.ThreadChangeResult{ThreadID: id, AddedLabels: []string{"STARRED"}, RemovedLabels: []string{"UNREAD"}}, nil
}
func (f *fakeMailProvider) CreateDraft(_ context.Context, input mailintegration.DraftInput) (mailintegration.Draft, error) {
	f.lastDraft = input
	return mailintegration.Draft{Provenance: mailintegration.Provenance{Provider: "gmail", ProviderID: "draft-1", AccountID: "account-1"}, ThreadID: input.ThreadID, Message: testMailMessage()}, nil
}
func (f *fakeMailProvider) UpdateDraft(_ context.Context, id string, input mailintegration.DraftInput) (mailintegration.Draft, error) {
	f.lastDraft = input
	return mailintegration.Draft{Provenance: mailintegration.Provenance{Provider: "gmail", ProviderID: id, AccountID: "account-1"}, ThreadID: input.ThreadID, Message: testMailMessage()}, nil
}
func (f *fakeMailProvider) SendDraft(context.Context, string) (mailintegration.Message, error) {
	f.sends.Add(1)
	return testMailMessage(), nil
}

func mailTestRouter(spaces *SpacesService) *chi.Mux {
	router := chi.NewRouter()
	router.Get("/mail/accounts", spaces.MailAccounts())
	router.Get("/mail/folders", spaces.MailFolders())
	router.Get("/mail/threads", spaces.MailThreads())
	router.Get("/mail/threads/{threadID}", spaces.MailThread())
	router.Post("/mail/threads/{threadID}/actions", spaces.MailThreadActions())
	router.Post("/mail/drafts", spaces.MailDrafts())
	router.Put("/mail/drafts/{draftID}", spaces.MailDraft())
	router.Post("/mail/drafts/{draftID}/send", spaces.MailSendDraft())
	return router
}

func TestMailHTTPContractAndSendConfirmation(t *testing.T) {
	database := openPresenceTestDatabase(t)
	owner, err := database.CreateUser("Mail Owner", uniqueTestEmail("mail-http"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("m", 32)))
	spaces, err := NewSpacesService(database, nil, key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := spaces.TestingEncryptConnectedAccountAccessToken("google", "secret-access-token")
	if err != nil {
		t.Fatal(err)
	}
	connection, err := database.SaveConnectedAccount(t.Context(), db.ConnectedAccount{UserID: owner.ID, Provider: "google", AccountID: "account-1", AccountDisplay: "owner@example.com", CredentialCiphertext: ciphertext, CredentialNonce: nonce, KeyVersion: 1, Capabilities: []string{"mail"}, GrantedScopes: []string{"gmail.modify", "gmail.send"}})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeMailProvider{}
	spaces.TestingSetMailProviderFactory(func(account db.ConnectedAccount, token string) (mailintegration.Provider, error) {
		if account.ID != connection.ID || token != "secret-access-token" {
			t.Fatalf("factory account/token = %q/%q", account.ID, token)
		}
		return fake, nil
	})
	router := mailTestRouter(spaces)
	token := newConversationTestBearerToken(t, database, owner.ID)
	request := func(method, path string, body any) map[string]any {
		recorder := performConversationRequest(t, router, method, path, token, body)
		if recorder.Code < 200 || recorder.Code >= 300 {
			t.Fatalf("%s %s status = %d body=%s", method, path, recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), "secret-access-token") || strings.Contains(recorder.Body.String(), "refresh-token") {
			t.Fatalf("response exposed credential: %s", recorder.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}

	accounts := request(http.MethodGet, "/mail/accounts", nil)
	if len(accounts["accounts"].([]any)) != 1 {
		t.Fatalf("accounts = %#v", accounts)
	}
	folders := request(http.MethodGet, "/mail/folders?connection_id="+connection.ID, nil)
	if len(folders["folders"].([]any)) != 1 {
		t.Fatalf("folders = %#v", folders)
	}
	threads := request(http.MethodGet, "/mail/threads?connection_id="+connection.ID+"&folder_id=INBOX&page_size=8", nil)
	if len(threads["threads"].([]any)) != 8 || threads["next_page_token"] != "next" || fake.maxGets.Load() != 0 {
		t.Fatalf("threads/concurrency = %#v / %d", threads, fake.maxGets.Load())
	}
	detail := request(http.MethodGet, "/mail/threads/thread-1?connection_id="+connection.ID, nil)
	thread := detail["thread"].(map[string]any)
	for _, field := range []string{"provider", "provider_id", "account_id", "last_message_at", "messages", "participants"} {
		if _, exists := thread[field]; !exists {
			t.Fatalf("thread missing %q: %#v", field, thread)
		}
	}
	action := request(http.MethodPost, "/mail/threads/thread-1/actions", map[string]any{"connection_id": connection.ID, "read": false, "starred": true})
	if action["thread_id"] != "thread-1" || fake.lastChanges.Read == nil || *fake.lastChanges.Read {
		t.Fatalf("action = %#v changes=%#v", action, fake.lastChanges)
	}
	draftInput := map[string]any{"connection_id": connection.ID, "thread_id": "thread-1", "to": []map[string]string{{"name": "B", "email": " b@example.com "}}, "cc": []any{}, "bcc": []any{}, "reply_to": []any{}, "subject": " Launch ", "text": "Ready", "attachments": []any{}}
	created := request(http.MethodPost, "/mail/drafts", draftInput)
	if created["draft"].(map[string]any)["provider_id"] != "draft-1" || fake.lastDraft.Subject != "Launch" || fake.lastDraft.To[0].Email != "b@example.com" {
		t.Fatalf("create draft = %#v normalized=%#v", created, fake.lastDraft)
	}
	updated := request(http.MethodPut, "/mail/drafts/draft-1", draftInput)
	if _, exists := updated["draft"].(map[string]any)["message"]; !exists {
		t.Fatalf("updated draft = %#v", updated)
	}

	rejected := performConversationRequest(t, router, http.MethodPost, "/mail/drafts/draft-1/send", token, map[string]any{"connection_id": connection.ID, "authoring_source": "ai", "confirmed": false})
	if rejected.Code != http.StatusConflict || fake.sends.Load() != 0 || !strings.Contains(rejected.Body.String(), "mail_confirmation_required") {
		t.Fatalf("unconfirmed AI send = %d %s sends=%d", rejected.Code, rejected.Body.String(), fake.sends.Load())
	}
	request(http.MethodPost, "/mail/drafts/draft-1/send", map[string]any{"connection_id": connection.ID, "authoring_source": "ai", "confirmed": true})
	request(http.MethodPost, "/mail/drafts/draft-1/send", map[string]any{"connection_id": connection.ID, "authoring_source": "user", "confirmed": false})
	if fake.sends.Load() != 2 {
		t.Fatalf("send count = %d, want 2", fake.sends.Load())
	}
}

func TestMailEndpointsRequireMailCapability(t *testing.T) {
	database := openPresenceTestDatabase(t)
	owner, _ := database.CreateUser("No Mail", uniqueTestEmail("no-mail"), "password123")
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("n", 32)))
	spaces, _ := NewSpacesService(database, nil, key)
	ciphertext, nonce, _ := spaces.TestingEncryptConnectedAccountAccessToken("google", "token")
	connection, _ := database.SaveConnectedAccount(t.Context(), db.ConnectedAccount{UserID: owner.ID, Provider: "google", AccountID: "account-2", AccountDisplay: "owner@example.com", CredentialCiphertext: ciphertext, CredentialNonce: nonce, KeyVersion: 1, Capabilities: []string{"calendar"}})
	recorder := performConversationRequest(t, mailTestRouter(spaces), http.MethodGet, "/mail/folders?connection_id="+connection.ID, newConversationTestBearerToken(t, database, owner.ID), nil)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "mail_capability_required") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMailAccountsKeepsAConnectionVisibleWhenItsProviderFails(t *testing.T) {
	database := openPresenceTestDatabase(t)
	owner, _ := database.CreateUser("Fallback Mail", uniqueTestEmail("fallback-mail"), "password123")
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("f", 32)))
	spaces, _ := NewSpacesService(database, nil, key)
	ciphertext, nonce, _ := spaces.TestingEncryptConnectedAccountAccessToken("google", "token")
	connection, _ := database.SaveConnectedAccount(t.Context(), db.ConnectedAccount{UserID: owner.ID,
		Provider: "google", AccountID: "fallback-account", AccountDisplay: "fallback@example.com",
		CredentialCiphertext: ciphertext, CredentialNonce: nonce, KeyVersion: 1, Capabilities: []string{"mail"}})
	spaces.TestingSetMailProviderFactory(func(db.ConnectedAccount, string) (mailintegration.Provider, error) {
		return &fakeMailProvider{accountError: &mailintegration.ProviderError{StatusCode: http.StatusTooManyRequests}}, nil
	})
	recorder := performConversationRequest(t, mailTestRouter(spaces), http.MethodGet, "/mail/accounts",
		newConversationTestBearerToken(t, database, owner.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Accounts []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || len(response.Accounts) != 1 {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	account := response.Accounts[0]
	if account["connection_id"] != connection.ID || account["provider"] != "google" ||
		account["status"] != "needs_attention" || account["error_code"] != "mail_provider_rate_limited" {
		t.Fatalf("fallback account=%#v", account)
	}
}

func TestMailAccountsClassifiesMicrosoftIdentityWithoutMailbox(t *testing.T) {
	database := openPresenceTestDatabase(t)
	owner, _ := database.CreateUser("No Mailbox", uniqueTestEmail("no-mailbox"), "password123")
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	spaces, _ := NewSpacesService(database, nil, key)
	ciphertext, nonce, _ := spaces.TestingEncryptConnectedAccountAccessToken("microsoft", "token")
	connection, _ := database.SaveConnectedAccount(t.Context(), db.ConnectedAccount{UserID: owner.ID,
		Provider: "microsoft", AccountID: "microsoft-account", AccountDisplay: "login@gmail.com",
		CredentialCiphertext: ciphertext, CredentialNonce: nonce, KeyVersion: 1, Capabilities: []string{"mail"}})
	spaces.TestingSetMailProviderFactory(func(db.ConnectedAccount, string) (mailintegration.Provider, error) {
		return &fakeMailProvider{folderError: &mailintegration.ProviderError{
			StatusCode: http.StatusBadRequest, Code: "MailboxNotEnabledForRESTAPI",
			Message: "The mailbox is not enabled.",
		}}, nil
	})
	recorder := performConversationRequest(t, mailTestRouter(spaces), http.MethodGet, "/mail/accounts",
		newConversationTestBearerToken(t, database, owner.ID), nil)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), connection.ID) ||
		!strings.Contains(recorder.Body.String(), "mail_provider_mailbox_unavailable") ||
		!strings.Contains(recorder.Body.String(), "needs_attention") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMailThreadsPersistsProviderAuthorizationFailure(t *testing.T) {
	database := openPresenceTestDatabase(t)
	owner, _ := database.CreateUser("Expired Mail", uniqueTestEmail("expired-mail"), "password123")
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("e", 32)))
	spaces, _ := NewSpacesService(database, nil, key)
	ciphertext, nonce, _ := spaces.TestingEncryptConnectedAccountAccessToken("google", "token")
	connection, _ := database.SaveConnectedAccount(t.Context(), db.ConnectedAccount{UserID: owner.ID,
		Provider: "google", AccountID: "expired-account", AccountDisplay: "expired@example.com",
		CredentialCiphertext: ciphertext, CredentialNonce: nonce, KeyVersion: 1, Capabilities: []string{"mail"}})
	spaces.TestingSetMailProviderFactory(func(db.ConnectedAccount, string) (mailintegration.Provider, error) {
		return &fakeMailProvider{threadError: &mailintegration.ProviderError{
			StatusCode: http.StatusUnauthorized,
		}}, nil
	})

	recorder := performConversationRequest(t, mailTestRouter(spaces), http.MethodGet,
		"/mail/threads?connection_id="+connection.ID,
		newConversationTestBearerToken(t, database, owner.ID), nil)
	if recorder.Code != http.StatusFailedDependency ||
		!strings.Contains(recorder.Body.String(), "mail_provider_authorization_failed") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	updated, err := database.ConnectedAccount(t.Context(), owner.ID, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "needs_attention" ||
		updated.LastErrorCode != "mail_provider_authorization_failed" {
		t.Fatalf("account health=%q/%q", updated.Status, updated.LastErrorCode)
	}
}
