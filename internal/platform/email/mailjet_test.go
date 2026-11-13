package email

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSendPostsOneMessageWithBasicAuth(t *testing.T) {
	var got struct {
		Messages []map[string]any `json:"Messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if r.Method != http.MethodPost || r.URL.Path != "/v3.1/send" || !ok || user != "key" || pass != "secret" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %s %s auth %v %q", r.Method, r.URL.Path, ok, user)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"Messages":[{"Status":"success","To":[{"Email":"a@example.com","MessageID":1}]}]}`))
	}))
	defer server.Close()
	c := &Client{APIKey: "key", SecretKey: "secret", FromEmail: "hello@mistysys.com", FromName: "Misty", BaseURL: server.URL, HTTP: server.Client()}
	err := c.Send(context.Background(), Message{To: "a@example.com", Subject: "Hi", Text: "plain", HTML: "<p>rich</p>", CustomID: "row-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("messages = %+v", got.Messages)
	}
	m := got.Messages[0]
	from, _ := m["From"].(map[string]any)
	to, _ := m["To"].([]any)
	first, _ := to[0].(map[string]any)
	if from["Email"] != "hello@mistysys.com" || from["Name"] != "Misty" || len(to) != 1 || first["Email"] != "a@example.com" ||
		m["Subject"] != "Hi" || m["TextPart"] != "plain" || m["HTMLPart"] != "<p>rich</p>" || m["CustomID"] != "row-1" {
		t.Fatalf("message = %+v", m)
	}
}

func TestSendReportsMailjetRefusals(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   string
	}{
		{400, `{"Messages":[{"Status":"error","Errors":[{"ErrorCode":"mj-0013","ErrorMessage":"is an invalid email address.","StatusCode":400}]}]}`, "mailjet answered 400 mj-0013: is an invalid email address."},
		{401, `{"ErrorIdentifier":"abc","ErrorMessage":"API key authentication/authorization failure","StatusCode":401}`, "mailjet answered 401 abc: API key authentication/authorization failure"},
		{200, `{"Messages":[{"Status":"error"}]}`, "mailjet answered 200: the answer did not confirm the message"},
		{503, `upstream down`, "mailjet answered 503"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		c := &Client{APIKey: "k", SecretKey: "s", FromEmail: "hello@mistysys.com", BaseURL: server.URL, HTTP: server.Client()}
		err := c.Send(context.Background(), Message{To: "a@example.com", Subject: "Hi", Text: "plain"})
		var refusal *Error
		if !errors.As(err, &refusal) || refusal.Status != tc.status || err.Error() != tc.want {
			t.Fatalf("%d: %v, want %q", tc.status, err, tc.want)
		}
		server.Close()
	}
}

func TestSendTimesOut(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release) // Before Close, which waits for the handler.
	c := &Client{APIKey: "k", SecretKey: "s", FromEmail: "hello@mistysys.com", BaseURL: server.URL, HTTP: &http.Client{Timeout: 50 * time.Millisecond}}
	if err := c.Send(context.Background(), Message{To: "a@example.com", Subject: "Hi", Text: "plain"}); err == nil || !strings.Contains(err.Error(), "mailjet unreachable") {
		t.Fatalf("timeout = %v", err)
	}
	if err := c.Send(context.Background(), Message{Subject: "Hi", Text: "plain"}); err == nil {
		t.Fatal("a message without a recipient was sent")
	}
}

func TestFromEnvNeedsAllThreeOrNone(t *testing.T) {
	for _, name := range []string{"MAILJET_API_KEY", "MAILJET_SECRET_KEY", "MAILJET_FROM_EMAIL", "MAILJET_FROM_NAME", "MAILJET_API_BASE_URL"} {
		t.Setenv(name, "")
	}
	if c, err := FromEnv(); c != nil || err != nil {
		t.Fatalf("unset = %v %v", c, err)
	}
	t.Setenv("MAILJET_API_KEY", "k")
	if _, err := FromEnv(); err == nil {
		t.Fatal("a partial configuration was accepted")
	}
	t.Setenv("MAILJET_SECRET_KEY", "s")
	t.Setenv("MAILJET_FROM_EMAIL", "Misty <hello@mistysys.com>")
	if _, err := FromEnv(); err == nil {
		t.Fatal("a named from address was accepted")
	}
	t.Setenv("MAILJET_FROM_EMAIL", "hello@mistysys.com")
	c, err := FromEnv()
	if err != nil || c.FromName != "Misty" || c.BaseURL != DefaultBaseURL || c.HTTP.Timeout == 0 {
		t.Fatalf("config = %+v %v", c, err)
	}
	t.Setenv("MAILJET_API_BASE_URL", "http://mailjet.example")
	if _, err := FromEnv(); err == nil {
		t.Fatal("a plain-http API URL was accepted")
	}
	t.Setenv("MAILJET_API_BASE_URL", "http://127.0.0.1:9999/")
	t.Setenv("MAILJET_FROM_NAME", "Misty Pro")
	if c, err = FromEnv(); err != nil || c.BaseURL != "http://127.0.0.1:9999" || c.FromName != "Misty Pro" {
		t.Fatalf("test config = %+v %v", c, err)
	}
}
