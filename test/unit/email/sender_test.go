package email

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/email"
)

func TestNewSenderFromEnvDefaultsToLogSender(t *testing.T) {
	t.Setenv("MAILJET_API_KEY", "")
	t.Setenv("MAILJET_SECRET_KEY", "")
	t.Setenv("MAILJET_FROM_EMAIL", "")

	sender, err := NewSenderFromEnv()
	if err != nil {
		t.Fatalf("NewSenderFromEnv() error = %v", err)
	}
	if _, ok := sender.(LogSender); !ok {
		t.Fatalf("sender type = %T, want LogSender", sender)
	}
}

func TestNewSenderFromEnvRequiresCompleteMailjetConfig(t *testing.T) {
	t.Setenv("MAILJET_API_KEY", "key")
	t.Setenv("MAILJET_SECRET_KEY", "")
	t.Setenv("MAILJET_FROM_EMAIL", "noreply@example.com")

	if _, err := NewSenderFromEnv(); err == nil {
		t.Fatal("NewSenderFromEnv() succeeded with partial Mailjet config")
	}
}

func TestNewSenderFromEnvBuildsMailjetSender(t *testing.T) {
	t.Setenv("MAILJET_API_KEY", "key")
	t.Setenv("MAILJET_SECRET_KEY", "secret")
	t.Setenv("MAILJET_FROM_EMAIL", "noreply@example.com")
	t.Setenv("MAILJET_FROM_NAME", "Misty")
	t.Setenv("MAILJET_API_BASE_URL", "https://mail.example.com/")

	sender, err := NewSenderFromEnv()
	if err != nil {
		t.Fatalf("NewSenderFromEnv() error = %v", err)
	}

	mailjetSender, ok := sender.(*MailjetSender)
	if !ok {
		t.Fatalf("sender type = %T, want *MailjetSender", sender)
	}
	if mailjetSender.TestingApiBaseURL != "https://mail.example.com" {
		t.Fatalf("apiBaseURL = %q, want %q", mailjetSender.TestingApiBaseURL, "https://mail.example.com")
	}
}

func TestMailjetSenderSendPasswordResetEmailSuccess(t *testing.T) {
	var authHeader string
	var requestBody TestingMailjetSendRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		if r.URL.Path != "/v3.1/send" {
			t.Fatalf("request path = %q, want %q", r.URL.Path, "/v3.1/send")
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_, _ = w.Write([]byte(`{"Messages":[{"Status":"success"}]}`))
	}))
	defer server.Close()

	sender := &MailjetSender{
		TestingApiBaseURL: server.URL,
		TestingApiKey:     "key",
		TestingSecretKey:  "secret",
		TestingFromEmail:  "noreply@example.com",
		TestingFromName:   "Misty",
		TestingHttpClient: server.Client(),
	}

	if err := sender.SendPasswordResetEmail(context.Background(), "user@example.com", "https://app.example.com/reset?token=abc"); err != nil {
		t.Fatalf("SendPasswordResetEmail() error = %v", err)
	}

	if !strings.HasPrefix(authHeader, "Basic ") {
		t.Fatalf("Authorization header = %q, want Basic auth", authHeader)
	}
	if len(requestBody.Messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(requestBody.Messages))
	}
	if requestBody.Messages[0].To[0].Email != "user@example.com" {
		t.Fatalf("recipient email = %q, want %q", requestBody.Messages[0].To[0].Email, "user@example.com")
	}
}

func TestMailjetSenderSendWaitlistNotificationEmailAllowsEmptyNotifyEmail(t *testing.T) {
	sender := &MailjetSender{}
	if err := sender.SendWaitlistNotificationEmail(context.Background(), "", "Ada", "ada@example.com"); err != nil {
		t.Fatalf("SendWaitlistNotificationEmail() error = %v", err)
	}
}

func TestMailjetSenderSendMessageFailures(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{name: "http_error", statusCode: http.StatusBadGateway, body: "upstream down", want: "mailjet password reset failed"},
		{name: "empty_messages", statusCode: http.StatusOK, body: `{"Messages":[]}`, want: "returned no messages"},
		{name: "non_success_status", statusCode: http.StatusOK, body: `{"Messages":[{"Status":"error","Errors":[{"ErrorCode":"mj-1","StatusCode":400,"ErrorMessage":"bad request"}]}]}`, want: `status "error"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			sender := &MailjetSender{
				TestingApiBaseURL: server.URL,
				TestingApiKey:     "key",
				TestingSecretKey:  "secret",
				TestingFromEmail:  "noreply@example.com",
				TestingHttpClient: server.Client(),
			}

			err := sender.SendPasswordResetEmail(context.Background(), "user@example.com", "https://app.example.com/reset")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("SendPasswordResetEmail() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestMailjetSenderInputValidation(t *testing.T) {
	sender := &MailjetSender{}

	if err := sender.SendPasswordResetEmail(context.Background(), "", "https://app.example.com/reset"); err == nil {
		t.Fatal("SendPasswordResetEmail() succeeded without recipient email")
	}
	if err := sender.SendPasswordResetEmail(context.Background(), "user@example.com", ""); err == nil {
		t.Fatal("SendPasswordResetEmail() succeeded without reset link")
	}
	if err := sender.SendWaitlistConfirmationEmail(context.Background(), "Ada", ""); err == nil {
		t.Fatal("SendWaitlistConfirmationEmail() succeeded without recipient email")
	}
	if err := sender.SendWaitlistNotificationEmail(context.Background(), "notify@example.com", "Ada", ""); err == nil {
		t.Fatal("SendWaitlistNotificationEmail() succeeded without waitlist email")
	}
}
