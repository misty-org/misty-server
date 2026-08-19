package mail_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mailbox "github.com/kannachi323/misty/server/internal/integrations/mail"
)

func TestOutlookReadsAccountAndPaginatedFolders(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assertGraphHeaders(t, request)
		switch request.URL.Path {
		case "/v1.0/me":
			if request.URL.Query().Get("$select") == "" {
				t.Error("profile select is missing")
			}
			fmt.Fprint(writer, `{"id":"user-1","displayName":"Ada Lovelace","mail":"ada@example.com"}`)
		case "/v1.0/me/mailFolders":
			if request.URL.Query().Get("$skiptoken") == "folder-next" {
				fmt.Fprint(writer, `{"value":[{"id":"custom-1","displayName":"Clients","totalItemCount":2}]}`)
				return
			}
			fmt.Fprintf(writer, `{"value":[{"id":"inbox-id","displayName":"Inbox","wellKnownName":"inbox","totalItemCount":9,"unreadItemCount":3},{"displayName":"broken"}],"@odata.nextLink":%q}`, server.URL+`/v1.0/me/mailFolders?$skiptoken=folder-next`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	provider := newOutlook(t, server.URL+"/v1.0", mailbox.OutlookConfig{AccountID: "connection-1"})
	account, err := provider.Account(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.Provider != mailbox.ProviderOutlook || account.ProviderID != "user-1" || account.AccountID != "connection-1" || account.Email != "ada@example.com" || account.DisplayName != "Ada Lovelace" {
		t.Fatalf("account = %#v", account)
	}
	folders, err := provider.ListFolders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 2 || folders[0].Kind != mailbox.FolderInbox || folders[0].Unread != 3 || folders[1].Kind != mailbox.FolderCustom {
		t.Fatalf("folders = %#v", folders)
	}
}

func TestOutlookPreservesProviderMailboxErrorCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(writer, `{"error":{"code":"MailboxNotEnabledForRESTAPI","message":"The mailbox is not enabled."}}`)
	}))
	defer server.Close()

	_, err := newOutlook(t, server.URL, mailbox.OutlookConfig{}).ListFolders(context.Background())
	var providerError *mailbox.ProviderError
	if !errors.As(err, &providerError) || providerError.Code != "MailboxNotEnabledForRESTAPI" {
		t.Fatalf("error = %#v", err)
	}
}

func TestOutlookListsMessagesGroupedByConversation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assertGraphHeaders(t, request)
		query := request.URL.Query()
		if query.Get("$top") != "25" || query.Get("$skiptoken") != "page-1" || query.Get("$search") != `"launch \"plan\""` {
			t.Errorf("query = %s", request.URL.RawQuery)
		}
		if query.Get("$filter") != "(parentFolderId eq 'folder''one' or parentFolderId eq 'folder-two')" {
			t.Errorf("filter = %q", query.Get("$filter"))
		}
		fmt.Fprint(writer, `{"value":[
          {"id":"m2","conversationId":"c1","subject":"Project","bodyPreview":"latest","sentDateTime":"2026-08-19T12:00:00Z","isRead":true,"flag":{"flagStatus":"flagged"},"from":{"emailAddress":{"name":"Ada","address":"ada@example.com"}}},
          {"id":"m1","conversationId":"c1","subject":"Project","bodyPreview":"first","sentDateTime":"2026-08-18T12:00:00Z","isRead":false,"toRecipients":[{"emailAddress":{"name":"Bob","address":"bob@example.com"}}]},
          {"id":"m3","conversationId":"c2","subject":"Other","bodyPreview":"other","sentDateTime":"2026-08-17T12:00:00Z","isRead":true}
        ],"@odata.nextLink":"https://graph.example/v1.0/me/messages?$skiptoken=next-page"}`)
	}))
	defer server.Close()

	provider := newOutlook(t, server.URL, mailbox.OutlookConfig{})
	page, err := provider.ListThreads(context.Background(), mailbox.ListThreadsRequest{
		PageSize: 25, PageToken: "page-1", Query: `launch "plan"`, FolderIDs: []string{"folder'one", "folder-two"},
	})
	if err == nil || !strings.Contains(err.Error(), "untrusted Microsoft Graph next link") {
		// The malicious next link must be rejected rather than followed with the bearer token.
		t.Fatalf("error = %v, page = %#v", err, page)
	}
}

func TestOutlookGroupsValidPageAndReturnsOpaqueSkipToken(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(writer, `{"value":[
          {"id":"m2","conversationId":"c1","subject":"Project","bodyPreview":"latest","sentDateTime":"2026-08-19T12:00:00Z","isRead":true,"flag":{"flagStatus":"flagged"}},
          {"id":"m1","conversationId":"c1","subject":"Project","bodyPreview":"first","sentDateTime":"2026-08-18T12:00:00Z","isRead":false},
          {"id":"m3","conversationId":"c2","subject":"Other","sentDateTime":"2026-08-17T12:00:00Z","isRead":true}
        ],"@odata.nextLink":%q}`, server.URL+`/me/messages?$skiptoken=opaque-token`)
	}))
	defer server.Close()
	page, err := newOutlook(t, server.URL, mailbox.OutlookConfig{}).ListThreads(context.Background(), mailbox.ListThreadsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Threads) != 2 || page.Threads[0].ProviderID != "c1" || len(page.Threads[0].Messages) != 2 || !page.Threads[0].Unread || !page.Threads[0].Starred || page.NextPageToken != "opaque-token" {
		t.Fatalf("page = %#v", page)
	}
}

func TestOutlookGetsConversationWithEscapedFilterAndSafeHTML(t *testing.T) {
	conversationID := "c' or isRead eq false or 'x"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.URL.Query().Get("$filter"); got != "conversationId eq 'c'' or isRead eq false or ''x'" {
			t.Errorf("filter = %q", got)
		}
		expansion := request.URL.Query().Get("$expand")
		if expansion == "" {
			t.Error("attachment expansion is missing")
		}
		if strings.Contains(expansion, "contentId") {
			t.Errorf("attachment expansion selects subtype-only contentId: %q", expansion)
		}
		if request.URL.Query().Get("$skiptoken") == "more" {
			fmt.Fprintf(writer, `{"value":[{"id":"m2","conversationId":%q,"body":{"contentType":"text","content":"Plain reply"},"sentDateTime":"2026-08-19T13:00:00Z","isRead":true}]}`, conversationID)
			return
		}
		fmt.Fprintf(writer, `{"value":[{"id":"m1","conversationId":%q,"internetMessageId":"<one@example.com>","subject":"Hello\r\nInjected","bodyPreview":"Preview","body":{"contentType":"html","content":"<div>Hello <b>world</b></div><script>bad()</script><iframe>secret</iframe>"},"sentDateTime":"2026-08-19T12:00:00Z","isRead":false,"flag":{"flagStatus":"flagged"},"from":{"emailAddress":{"name":"Ada","address":"ada@example.com"}},"attachments":[{"id":"att-1","name":"chart.png","contentType":"image/png","size":12,"isInline":true,"contentId":"chart"}]}],"@odata.nextLink":%q}`, conversationID, server.URL+`/me/messages?$skiptoken=more`)
	}))
	defer server.Close()

	thread, err := newOutlook(t, server.URL, mailbox.OutlookConfig{AccountID: "acct"}).GetThread(context.Background(), conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if thread.ProviderID != conversationID || len(thread.Messages) != 2 || thread.Subject != "Hello  Injected" || !thread.Unread || !thread.Starred {
		t.Fatalf("thread = %#v", thread)
	}
	message := thread.Messages[0]
	if message.Body.Text != "Hello world" || !message.Body.HadHTML || strings.Contains(message.Body.Text, "bad") || message.AccountID != "acct" {
		t.Fatalf("message = %#v", message)
	}
	if len(message.Attachments) != 1 || message.Attachments[0].ProviderID != "att-1" || !message.Attachments[0].Inline {
		t.Fatalf("attachments = %#v", message.Attachments)
	}
}

func TestOutlookRejectsMalformedOversizedAndSlowProviders(t *testing.T) {
	t.Run("malformed JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, `{"id":`)
		}))
		defer server.Close()
		_, err := newOutlook(t, server.URL, mailbox.OutlookConfig{}).Account(context.Background())
		if err == nil || !strings.Contains(err.Error(), "decode Microsoft Graph response") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("malformed identity", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, `{"value":[{"id":"m-without-conversation"}]}`)
		}))
		defer server.Close()
		_, err := newOutlook(t, server.URL, mailbox.OutlookConfig{}).ListThreads(context.Background(), mailbox.ListThreadsRequest{})
		if err == nil || !strings.Contains(err.Error(), "identity") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("response limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, strings.Repeat("x", 65))
		}))
		defer server.Close()
		_, err := newOutlook(t, server.URL, mailbox.OutlookConfig{MaxResponseBytes: 64}).Account(context.Background())
		if !errors.Is(err, mailbox.ErrResponseTooLarge) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("body limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, `{"value":[{"id":"m","conversationId":"c","body":{"contentType":"text","content":"12345"}}]}`)
		}))
		defer server.Close()
		_, err := newOutlook(t, server.URL, mailbox.OutlookConfig{MaxBodyBytes: 4}).GetThread(context.Background(), "c")
		if !errors.Is(err, mailbox.ErrBodyTooLarge) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			<-request.Context().Done()
		}))
		defer server.Close()
		started := time.Now()
		_, err := newOutlook(t, server.URL, mailbox.OutlookConfig{Timeout: 20 * time.Millisecond}).Account(context.Background())
		if err == nil || time.Since(started) > time.Second {
			t.Fatalf("error = %v elapsed = %s", err, time.Since(started))
		}
	})
}

func TestOutlookDoesNotFollowBearerRedirects(t *testing.T) {
	targetRequests := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests++
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL+"/steal", http.StatusFound)
	}))
	defer redirect.Close()
	permissiveClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
	_, err := newOutlook(t, redirect.URL, mailbox.OutlookConfig{HTTPClient: permissiveClient}).Account(context.Background())
	var providerError *mailbox.ProviderError
	if !errors.As(err, &providerError) || providerError.StatusCode != http.StatusFound || targetRequests != 0 {
		t.Fatalf("error = %#v target requests = %d", err, targetRequests)
	}
}

func newOutlook(t *testing.T, baseURL string, extra mailbox.OutlookConfig) *mailbox.Outlook {
	t.Helper()
	extra.BaseURL = baseURL
	extra.AccessToken = "graph-secret"
	if extra.Timeout == 0 {
		extra.Timeout = time.Second
	}
	provider, err := mailbox.NewOutlook(extra)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func assertGraphHeaders(t *testing.T, request *http.Request) {
	t.Helper()
	if request.Header.Get("Authorization") != "Bearer graph-secret" || request.Header.Get("Prefer") != `IdType="ImmutableId"` {
		t.Errorf("headers = %#v", request.Header)
	}
}
