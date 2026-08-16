package mail_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	mailbox "github.com/kannachi323/misty/server/internal/integrations/mail"
)

func TestOutlookModifiesEveryMessageInConversation(t *testing.T) {
	var mutex sync.Mutex
	requests := make([]string, 0, 5)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assertGraphHeaders(t, request)
		if request.Method == http.MethodGet {
			if request.URL.Query().Get("$filter") != "conversationId eq 'conversation''one'" {
				t.Errorf("filter = %q", request.URL.Query().Get("$filter"))
			}
			fmt.Fprint(writer, `{"value":[{"id":"m1","conversationId":"conversation'one"},{"id":"m2","conversationId":"conversation'one"}]}`)
			return
		}
		mutex.Lock()
		requests = append(requests, request.Method+" "+request.URL.Path)
		mutex.Unlock()
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		if request.Method == http.MethodPatch {
			if payload["isRead"] != true {
				t.Errorf("patch = %#v", payload)
			}
			flag, ok := payload["flag"].(map[string]any)
			if !ok || flag["flagStatus"] != "flagged" {
				t.Errorf("flag = %#v", payload["flag"])
			}
		} else if payload["destinationId"] != "archive" {
			t.Errorf("move = %#v", payload)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	read, archived, starred := true, true, true
	result, err := newOutlook(t, server.URL, mailbox.OutlookConfig{}).ModifyThread(context.Background(), "conversation'one", mailbox.ThreadChanges{
		Read: &read, Archived: &archived, Starred: &starred,
	})
	if err != nil {
		t.Fatal(err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	want := "PATCH /me/messages/m1,POST /me/messages/m1/move,PATCH /me/messages/m2,POST /me/messages/m2/move"
	if strings.Join(requests, ",") != want {
		t.Fatalf("requests = %#v", requests)
	}
	if result.ThreadID != "conversation'one" || strings.Join(result.AddedLabels, ",") != "ARCHIVED,STARRED" || strings.Join(result.RemovedLabels, ",") != "UNREAD" {
		t.Fatalf("result = %#v", result)
	}
}

func TestOutlookCanMarkUnreadUnarchiveAndUnstar(t *testing.T) {
	var patch map[string]any
	destination := ""
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			fmt.Fprint(writer, `{"value":[{"id":"m1","conversationId":"c1"}]}`)
		case http.MethodPatch:
			if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
				t.Error(err)
			}
			writer.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			var move map[string]string
			if err := json.NewDecoder(request.Body).Decode(&move); err != nil {
				t.Error(err)
			}
			destination = move["destinationId"]
			writer.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	read, archived, starred := false, false, false
	result, err := newOutlook(t, server.URL, mailbox.OutlookConfig{}).ModifyThread(context.Background(), "c1", mailbox.ThreadChanges{
		Read: &read, Archived: &archived, Starred: &starred,
	})
	if err != nil {
		t.Fatal(err)
	}
	flag, _ := patch["flag"].(map[string]any)
	if patch["isRead"] != false || flag["flagStatus"] != "notFlagged" || destination != "inbox" {
		t.Fatalf("patch = %#v destination = %q", patch, destination)
	}
	if strings.Join(result.AddedLabels, ",") != "UNREAD" || strings.Join(result.RemovedLabels, ",") != "ARCHIVED,STARRED" {
		t.Fatalf("result = %#v", result)
	}
}

func TestOutlookCreatesDraftWithRecipientsAndAttachment(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Method == http.MethodGet {
			fmt.Fprint(writer, graphDraftJSON("draft-1"))
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		if request.URL.Path == "/me/messages" {
			body, ok := payload["body"].(map[string]any)
			if !ok || body["contentType"] != "Text" || body["content"] != "Draft text" {
				t.Errorf("body = %#v", payload["body"])
			}
			recipients, ok := payload["toRecipients"].([]any)
			if !ok || len(recipients) != 1 || payload["attachments"] != nil {
				t.Errorf("draft payload = %#v", payload)
			}
			fmt.Fprint(writer, graphDraftJSON("draft-1"))
			return
		}
		if request.URL.Path != "/me/messages/draft-1/attachments" || payload["@odata.type"] != "#microsoft.graph.fileAttachment" || payload["contentBytes"] == "" {
			t.Errorf("attachment request = %s %#v", request.URL.Path, payload)
		}
		fmt.Fprint(writer, `{}`)
	}))
	defer server.Close()
	provider := newOutlook(t, server.URL, mailbox.OutlookConfig{AccountID: "acct"})
	draft, err := provider.CreateDraft(context.Background(), mailbox.DraftInput{
		To: []mailbox.Address{{Name: "Ada", Email: "ada@example.com"}}, Subject: "Hello", Text: "Draft text",
		Attachments: []mailbox.DraftAttachment{{Filename: "notes.txt", ContentType: "text/plain", Data: []byte("notes")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 3 || draft.Provider != mailbox.ProviderOutlook || draft.ProviderID != "draft-1" || draft.AccountID != "acct" || !draft.Message.Draft {
		t.Fatalf("draft = %#v requests = %d", draft, requests)
	}
}

func TestOutlookUpdatesDraftAndRefetchesAfterEmptyResponse(t *testing.T) {
	requests := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		if request.Method == http.MethodPatch {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.HasSuffix(request.URL.Path, "/attachments") {
			fmt.Fprint(writer, `{"value":[]}`)
			return
		}
		if request.Method != http.MethodGet || request.URL.Query().Get("$select") == "" {
			t.Errorf("request = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
		}
		fmt.Fprint(writer, graphDraftJSON("draft-2"))
	}))
	defer server.Close()
	draft, err := newOutlook(t, server.URL, mailbox.OutlookConfig{}).UpdateDraft(context.Background(), "draft-2", mailbox.DraftInput{Subject: "Updated"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(requests, ",") != "PATCH /me/messages/draft-2,GET /me/messages/draft-2/attachments,GET /me/messages/draft-2" || draft.ProviderID != "draft-2" {
		t.Fatalf("requests = %#v draft = %#v", requests, draft)
	}
}

func TestOutlookSendsOnlyAnExistingDraft(t *testing.T) {
	requests := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		if request.Method == http.MethodGet {
			fmt.Fprint(writer, graphDraftJSON("draft-9"))
			return
		}
		if request.Method != http.MethodPost || request.URL.Path != "/me/messages/draft-9/send" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.ContentLength > 0 {
			t.Errorf("send content length = %d", request.ContentLength)
		}
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	message, err := newOutlook(t, server.URL, mailbox.OutlookConfig{}).SendDraft(context.Background(), "draft-9")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(requests, ",") != "GET /me/messages/draft-9,POST /me/messages/draft-9/send" || message.ProviderID != "draft-9" || message.Draft {
		t.Fatalf("requests = %#v message = %#v", requests, message)
	}
}

func TestOutlookRefusesToSendNonDraftAndValidatesWrites(t *testing.T) {
	t.Run("not a draft", func(t *testing.T) {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			requests++
			fmt.Fprint(writer, `{"id":"m1","conversationId":"c1","isDraft":false}`)
		}))
		defer server.Close()
		_, err := newOutlook(t, server.URL, mailbox.OutlookConfig{}).SendDraft(context.Background(), "m1")
		if !errors.Is(err, mailbox.ErrInvalidInput) || requests != 1 {
			t.Fatalf("error = %v requests = %d", err, requests)
		}
	})
	t.Run("header injection", func(t *testing.T) {
		provider := newOutlook(t, "https://example.test", mailbox.OutlookConfig{})
		_, err := provider.CreateDraft(context.Background(), mailbox.DraftInput{
			To: []mailbox.Address{{Email: "safe@example.com\r\nattacker@example.com"}},
		})
		if !errors.Is(err, mailbox.ErrInvalidInput) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("aggregate body limit", func(t *testing.T) {
		provider := newOutlook(t, "https://example.test", mailbox.OutlookConfig{MaxBodyBytes: 5})
		_, err := provider.CreateDraft(context.Background(), mailbox.DraftInput{
			Text: "123", Attachments: []mailbox.DraftAttachment{{Filename: "a", Data: []byte("456")}},
		})
		if !errors.Is(err, mailbox.ErrBodyTooLarge) {
			t.Fatalf("error = %v", err)
		}
	})
}

func graphDraftJSON(id string) string {
	return fmt.Sprintf(`{"id":%q,"conversationId":"conversation-1","subject":"Draft","bodyPreview":"preview","body":{"contentType":"text","content":"Draft text"},"isDraft":true,"toRecipients":[{"emailAddress":{"name":"Ada","address":"ada@example.com"}}],"attachments":[{"id":"attachment-1","name":"notes.txt","contentType":"text/plain","size":5}]}`, id)
}
