package mail_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	stdmail "net/mail"
	"strings"
	"sync"
	"testing"
	"time"

	mailbox "github.com/kannachi323/misty/server/internal/integrations/mail"
)

func TestGmailModifiesThreadLabels(t *testing.T) {
	var captured struct {
		Add    []string `json:"addLabelIds"`
		Remove []string `json:"removeLabelIds"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/users/me/threads/thread-1/modify" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Error(err)
		}
		fmt.Fprint(writer, `{}`)
	}))
	defer server.Close()
	provider := newGmail(t, server.URL, mailbox.GmailConfig{})
	read, archived, starred := true, true, true
	result, err := provider.ModifyThread(context.Background(), "thread-1", mailbox.ThreadChanges{
		Read: &read, Archived: &archived, Starred: &starred,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(captured.Add, ",") != "STARRED" || strings.Join(captured.Remove, ",") != "UNREAD,INBOX" {
		t.Fatalf("payload = %#v", captured)
	}
	if result.ThreadID != "thread-1" || strings.Join(result.AddedLabels, ",") != "STARRED" {
		t.Fatalf("result = %#v", result)
	}
	if _, err := provider.ModifyThread(context.Background(), "thread-1", mailbox.ThreadChanges{}); !errors.Is(err, mailbox.ErrInvalidInput) {
		t.Fatalf("empty change error = %v", err)
	}
}

func TestGmailCreatesAndUpdatesDraftWithoutSending(t *testing.T) {
	var mutex sync.Mutex
	requests := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		requests = append(requests, request.Method+" "+request.URL.Path)
		mutex.Unlock()
		var payload struct {
			ID      string `json:"id"`
			Message struct {
				Raw      string `json:"raw"`
				ThreadID string `json:"threadId"`
			} `json:"message"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		raw, err := base64.RawURLEncoding.DecodeString(payload.Message.Raw)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := stdmail.ReadMessage(strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		to, addressErr := stdmail.ParseAddress(parsed.Header.Get("To"))
		if addressErr != nil || to.Address != "ada@example.com" || to.Name != "Ada" || parsed.Header.Get("Subject") == "" {
			t.Errorf("draft headers = %#v", parsed.Header)
		}
		contents, _ := io.ReadAll(parsed.Body)
		if !strings.Contains(string(contents), "notes.txt") {
			t.Errorf("multipart body does not contain attachment metadata: %s", contents)
		}
		if request.Method == http.MethodPut && payload.ID != "draft-1" {
			t.Errorf("update draft id = %q", payload.ID)
		}
		if payload.Message.ThreadID != "thread-1" {
			t.Errorf("thread id = %q", payload.Message.ThreadID)
		}
		fmt.Fprint(writer, `{"id":"draft-1","message":{"id":"message-1","threadId":"thread-1","labelIds":["DRAFT"],"payload":{"headers":[{"name":"Subject","value":"Hello"}]}}}`)
	}))
	defer server.Close()
	provider := newGmail(t, server.URL, mailbox.GmailConfig{})
	input := mailbox.DraftInput{
		ThreadID: "thread-1", To: []mailbox.Address{{Name: "Ada", Email: "ada@example.com"}}, Subject: "Hello ✓", Text: "Draft text",
		Attachments: []mailbox.DraftAttachment{{Filename: "notes.txt", ContentType: "text/plain", Data: []byte("attachment")}},
	}
	created, err := provider.CreateDraft(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if created.ProviderID != "draft-1" || created.Message.ProviderID != "message-1" || !created.Message.Draft {
		t.Fatalf("created = %#v", created)
	}
	updated, err := provider.UpdateDraft(context.Background(), "draft-1", input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProviderID != "draft-1" {
		t.Fatalf("updated = %#v", updated)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if strings.Join(requests, ",") != "POST /users/me/drafts,PUT /users/me/drafts/draft-1" {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestGmailSendsOnlyAnExistingDraftID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/users/me/drafts/send" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		var payload map[string]string
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload) != 1 || payload["id"] != "draft-9" {
			t.Errorf("send payload = %#v", payload)
		}
		fmt.Fprint(writer, `{"id":"sent-1","threadId":"thread-1","labelIds":["SENT"],"payload":{"headers":[{"name":"To","value":"ada@example.com"}]}}`)
	}))
	defer server.Close()
	message, err := newGmail(t, server.URL, mailbox.GmailConfig{}).SendDraft(context.Background(), "draft-9")
	if err != nil {
		t.Fatal(err)
	}
	if message.ProviderID != "sent-1" || message.ThreadID != "thread-1" || len(message.To) != 1 {
		t.Fatalf("message = %#v", message)
	}
}

func TestGmailDraftValidationAndProviderErrors(t *testing.T) {
	t.Run("header injection", func(t *testing.T) {
		provider := newGmail(t, "https://example.test", mailbox.GmailConfig{})
		_, err := provider.CreateDraft(context.Background(), mailbox.DraftInput{
			To: []mailbox.Address{{Email: "safe@example.com\r\nBcc: attacker@example.com"}},
		})
		if !errors.Is(err, mailbox.ErrInvalidInput) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("aggregate body limit", func(t *testing.T) {
		provider := newGmail(t, "https://example.test", mailbox.GmailConfig{MaxBodyBytes: 5})
		_, err := provider.CreateDraft(context.Background(), mailbox.DraftInput{
			Text: "123", Attachments: []mailbox.DraftAttachment{{Filename: "a", Data: []byte("456")}},
		})
		if !errors.Is(err, mailbox.ErrBodyTooLarge) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("provider error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(writer, `{"error":{"message":"slow down"}}`)
		}))
		defer server.Close()
		_, err := newGmail(t, server.URL, mailbox.GmailConfig{}).Account(context.Background())
		var providerError *mailbox.ProviderError
		if !errors.As(err, &providerError) || providerError.StatusCode != http.StatusTooManyRequests || !strings.Contains(err.Error(), "slow down") {
			t.Fatalf("error = %#v", err)
		}
	})
}

func TestGmailAppliesPerRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	provider := newGmail(t, server.URL, mailbox.GmailConfig{Timeout: 20 * time.Millisecond})
	started := time.Now()
	_, err := provider.Account(context.Background())
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("timeout error = %v, elapsed = %s", err, time.Since(started))
	}
}
