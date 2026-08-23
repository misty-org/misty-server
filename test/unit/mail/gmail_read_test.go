package mail_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mailbox "github.com/kannachi323/misty/server/internal/integrations/mail"
)

func TestGmailReadsAccountFoldersAndThreadPage(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret-token" {
			t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/gmail/v1/users/me/profile":
			fmt.Fprint(writer, `{"emailAddress":"person@example.com","messagesTotal":42,"threadsTotal":12}`)
		case "/gmail/v1/users/me/labels":
			fmt.Fprint(writer, `{"labels":[{"id":"INBOX","name":"Inbox","type":"system","threadsTotal":9,"threadsUnread":3},{"id":"Label_7","name":"Clients","type":"user"},{"name":"broken"}]}`)
		case "/gmail/v1/users/me/threads":
			if request.URL.Query().Get("maxResults") != "25" || request.URL.Query().Get("pageToken") != "next" || request.URL.Query().Get("q") != "from:ada" {
				t.Errorf("unexpected query: %s", request.URL.RawQuery)
			}
			if got := request.URL.Query()["labelIds"]; len(got) != 2 || got[0] != "INBOX" || got[1] != "STARRED" {
				t.Errorf("labels = %#v", got)
			}
			fmt.Fprint(writer, `{"threads":[{"id":"t1","snippet":" hello\u0000 "},{"snippet":"broken"}],"nextPageToken":"after","resultSizeEstimate":7}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	provider := newGmail(t, server.URL+"/gmail/v1", mailbox.GmailConfig{AccountID: "connection-1"})
	account, err := provider.Account(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.Email != "person@example.com" || account.Total != 42 || account.ProviderID != "person@example.com" || account.AccountID != "connection-1" {
		t.Fatalf("account = %#v", account)
	}
	folders, err := provider.ListFolders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 2 || folders[0].Kind != mailbox.FolderInbox || folders[0].Unread != 3 || folders[1].Kind != mailbox.FolderCustom {
		t.Fatalf("folders = %#v", folders)
	}
	page, err := provider.ListThreads(context.Background(), mailbox.ListThreadsRequest{
		PageSize: 25, PageToken: "next", Query: "from:ada", FolderIDs: []string{"INBOX", "STARRED"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Threads) != 1 || page.Threads[0].ProviderID != "t1" || page.Threads[0].Snippet != "hello" || page.NextPageToken != "after" || page.EstimatedTotal != 7 {
		t.Fatalf("page = %#v", page)
	}
}

func TestGmailNormalizesMIMEAndPreservesHTML(t *testing.T) {
	plain := base64.RawURLEncoding.EncodeToString([]byte("Hello from plain.\x00"))
	html := base64.RawURLEncoding.EncodeToString([]byte(`<div>HTML copy<script>alert(1)</script></div>`))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("format") != "full" {
			t.Errorf("format = %q", request.URL.Query().Get("format"))
		}
		fmt.Fprintf(writer, `{
          "id":"thread-9","snippet":"Latest", "messages":[{
            "id":"message-1","threadId":"thread-9","internalDate":"1767225600000",
            "labelIds":["UNREAD","STARRED","INBOX"],"snippet":"Preview",
            "payload":{"mimeType":"multipart/mixed","headers":[
              {"name":"Subject","value":"=?UTF-8?Q?Project_=E2=9C=93?="},
              {"name":"From","value":"Ada <ada@example.com>"},
              {"name":"To","value":"Bob <bob@example.com>"},
              {"name":"Date","value":"Thu, 1 Jan 2026 12:00:00 +0000"},
              {"name":"Message-ID","value":"<rfc-1@example.com>"},
              {"name":"X-Untrusted","value":"not exposed"}],
              "parts":[
                {"partId":"0.0","mimeType":"text/plain","body":{"data":%q}},
                {"partId":"0.1","mimeType":"text/html","body":{"data":%q}},
                {"partId":"1","mimeType":"image/png","filename":"chart.png","headers":[{"name":"Content-Disposition","value":"inline"},{"name":"Content-ID","value":"<chart>"}],"body":{"attachmentId":"att-1","size":1234}}
              ]}
          }]}`, plain, html)
	}))
	defer server.Close()

	provider := newGmail(t, server.URL, mailbox.GmailConfig{AccountID: "acct"})
	thread, err := provider.GetThread(context.Background(), "thread-9")
	if err != nil {
		t.Fatal(err)
	}
	if thread.Subject != "Project ✓" || !thread.Unread || !thread.Starred || len(thread.Messages) != 1 {
		t.Fatalf("thread = %#v", thread)
	}
	message := thread.Messages[0]
	if message.Body.Text != "Hello from plain." || !message.Body.HadHTML || !strings.Contains(message.Body.HTML, "HTML copy") {
		t.Fatalf("body = %#v", message.Body)
	}
	if message.From.Email != "ada@example.com" || len(message.To) != 1 || message.RFC822ID != "<rfc-1@example.com>" {
		t.Fatalf("headers = %#v", message)
	}
	if len(message.Attachments) != 1 || message.Attachments[0].ProviderID != "att-1" || !message.Attachments[0].Inline || message.Attachments[0].ContentID != "chart" {
		t.Fatalf("attachments = %#v", message.Attachments)
	}
}

func TestGmailConvertsHTMLOnlyMessageToInertText(t *testing.T) {
	html := base64.RawURLEncoding.EncodeToString([]byte(`<div>Hello <b>world</b></div><iframe>secret</iframe><style>.bad{}</style>`))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(writer, `{"id":"t","messages":[{"id":"m","payload":{"mimeType":"text/html","body":{"data":%q}}}]}`, html)
	}))
	defer server.Close()
	thread, err := newGmail(t, server.URL, mailbox.GmailConfig{}).GetThread(context.Background(), "t")
	if err != nil {
		t.Fatal(err)
	}
	if got := thread.Messages[0].Body.Text; got != "Hello world" || strings.Contains(got, "secret") {
		t.Fatalf("HTML text = %q", got)
	}
	if gotHTML := thread.Messages[0].Body.HTML; !strings.Contains(gotHTML, "Hello <b>world</b>") {
		t.Fatalf("HTML body = %q", gotHTML)
	}
}

func TestGmailRejectsMalformedAndOversizedResponses(t *testing.T) {
	t.Run("malformed JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, `{"emailAddress":`)
		}))
		defer server.Close()
		_, err := newGmail(t, server.URL, mailbox.GmailConfig{}).Account(context.Background())
		if err == nil || !strings.Contains(err.Error(), "decode gmail response") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("malformed MIME", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, `{"id":"t","messages":[{"id":"m","payload":{"mimeType":"text/plain","body":{"data":"%%%"}}}]}`)
		}))
		defer server.Close()
		_, err := newGmail(t, server.URL, mailbox.GmailConfig{}).GetThread(context.Background(), "t")
		if err == nil || !strings.Contains(err.Error(), "malformed gmail MIME") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("oversized response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, strings.Repeat("x", 65))
		}))
		defer server.Close()
		_, err := newGmail(t, server.URL, mailbox.GmailConfig{MaxResponseBytes: 64}).Account(context.Background())
		if !errors.Is(err, mailbox.ErrResponseTooLarge) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("oversized decoded body", func(t *testing.T) {
		data := base64.RawURLEncoding.EncodeToString([]byte("12345"))
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(writer, `{"id":"t","messages":[{"id":"m","payload":{"mimeType":"text/plain","body":{"data":%q}}}]}`, data)
		}))
		defer server.Close()
		_, err := newGmail(t, server.URL, mailbox.GmailConfig{MaxBodyBytes: 4}).GetThread(context.Background(), "t")
		if !errors.Is(err, mailbox.ErrBodyTooLarge) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestGmailListThreadsHydratesMetadataAndUnescapesHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/gmail/v1/users/me/threads":
			fmt.Fprint(writer, `{"threads":[{"id":"t-123","snippet":"Don&#39;t miss $29+ jeans &amp; FREE ship*"}],"resultSizeEstimate":1}`)
		case "/gmail/v1/users/me/threads/t-123":
			if request.URL.Query().Get("format") != "metadata" {
				t.Errorf("format = %q, want metadata", request.URL.Query().Get("format"))
			}
			fmt.Fprint(writer, `{
				"id":"t-123",
				"snippet":"Don&#39;t miss $29+ jeans &amp; FREE ship*",
				"messages":[{
					"id":"msg-1",
					"threadId":"t-123",
					"internalDate":"1755950400000",
					"labelIds":["UNREAD","INBOX"],
					"snippet":"Don&#39;t miss $29+ jeans &amp; FREE ship*",
					"payload":{
						"headers":[
							{"name":"Subject","value":"New Arrivals &amp; Discounts"},
							{"name":"From","value":"Pacsun &lt;deals@pacsun.com&gt;"},
							{"name":"To","value":"User &lt;user@example.com&gt;"},
							{"name":"Cc","value":"Rewards &lt;rewards@pacsun.com&gt;"},
							{"name":"Date","value":"Sun, 23 Aug 2026 12:00:00 +0000"}
						]
					}
				}]
			}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	provider := newGmail(t, server.URL+"/gmail/v1", mailbox.GmailConfig{AccountID: "conn-1"})
	page, err := provider.ListThreads(context.Background(), mailbox.ListThreadsRequest{PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Threads) != 1 {
		t.Fatalf("got %d threads, want 1", len(page.Threads))
	}
	th := page.Threads[0]
	if th.ProviderID != "t-123" {
		t.Errorf("providerID = %q", th.ProviderID)
	}
	if th.Subject != "New Arrivals & Discounts" {
		t.Errorf("subject = %q, want unescaped subject", th.Subject)
	}
	if th.Snippet != "Don't miss $29+ jeans & FREE ship*" {
		t.Errorf("snippet = %q, want unescaped snippet", th.Snippet)
	}
	// Participants must contain From (Pacsun) and Cc (Rewards), but not To (User).
	if len(th.Participants) != 2 || th.Participants[0].Name != "Pacsun" || th.Participants[1].Name != "Rewards" {
		t.Errorf("participants = %#v", th.Participants)
	}
	if !th.Unread {
		t.Errorf("unread = false, want true")
	}
}

func newGmail(t *testing.T, baseURL string, extra mailbox.GmailConfig) *mailbox.Gmail {
	t.Helper()
	extra.BaseURL = baseURL
	extra.AccessToken = "secret-token"
	if extra.Timeout == 0 {
		extra.Timeout = time.Second
	}
	provider, err := mailbox.NewGmail(extra)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}
