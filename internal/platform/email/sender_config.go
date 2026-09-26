package email

import (
	"context"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const defaultMailjetAPIBaseURL = "https://api.mailjet.com"

type PasswordResetSender interface {
	SendPasswordResetEmail(ctx context.Context, recipientEmail, resetLink string) error
}

type SpaceInvitationSender interface {
	SendSpaceInvitationEmail(
		ctx context.Context,
		recipientEmail, inviterName, spaceName, invitationLink string,
	) error
}

type Sender interface {
	PasswordResetSender
	SpaceInvitationSender
}

type LogSender struct{}

func (LogSender) SendPasswordResetEmail(_ context.Context, recipientEmail, resetLink string) error {
	log.Printf("password reset email to %s: %s", recipientEmail, resetLink)
	return nil
}

func (LogSender) SendSpaceInvitationEmail(
	_ context.Context,
	recipientEmail, inviterName, spaceName, invitationLink string,
) error {
	log.Printf(
		"Space invitation email to %s from %s for %s: %s",
		recipientEmail, inviterName, spaceName, invitationLink,
	)
	return nil
}

type MailjetSender struct {
	TestingApiBaseURL string
	TestingApiKey     string
	TestingSecretKey  string
	TestingFromEmail  string
	TestingFromName   string
	TestingHttpClient *http.Client
}

type TestingMailjetSendRequest struct {
	Messages []mailjetMessage `json:"Messages"`
}

type mailjetMessage struct {
	From     mailjetContact   `json:"From"`
	To       []mailjetContact `json:"To"`
	Subject  string           `json:"Subject"`
	TextPart string           `json:"TextPart"`
	HTMLPart string           `json:"HTMLPart"`
}

type mailjetContact struct {
	Email string `json:"Email"`
	Name  string `json:"Name,omitempty"`
}

type mailjetSendResponse struct {
	Messages []mailjetMessageResult `json:"Messages"`
}

type mailjetMessageResult struct {
	Status string `json:"Status"`
	Errors []struct {
		ErrorIdentifier string `json:"ErrorIdentifier"`
		ErrorCode       string `json:"ErrorCode"`
		StatusCode      int    `json:"StatusCode"`
		ErrorMessage    string `json:"ErrorMessage"`
	} `json:"Errors"`
	To []struct {
		MessageUUID string `json:"MessageUUID"`
		MessageID   int64  `json:"MessageID"`
		MessageHref string `json:"MessageHref"`
	} `json:"To"`
}

func NewSenderFromEnv() (Sender, error) {
	apiKey := strings.TrimSpace(envconfig.Getenv("MAILJET_API_KEY"))
	secretKey := strings.TrimSpace(envconfig.Getenv("MAILJET_SECRET_KEY"))
	fromEmail := strings.TrimSpace(envconfig.Getenv("MAILJET_FROM_EMAIL"))

	if apiKey == "" && secretKey == "" && fromEmail == "" {
		return LogSender{}, nil
	}
	if apiKey == "" || secretKey == "" || fromEmail == "" {
		return nil, fmt.Errorf("MAILJET_API_KEY, MAILJET_SECRET_KEY, and MAILJET_FROM_EMAIL must all be set")
	}

	apiBaseURL := strings.TrimRight(strings.TrimSpace(envconfig.Getenv("MAILJET_API_BASE_URL")), "/")
	if apiBaseURL == "" {
		apiBaseURL = defaultMailjetAPIBaseURL
	}

	return &MailjetSender{
		TestingApiBaseURL: apiBaseURL,
		TestingApiKey:     apiKey,
		TestingSecretKey:  secretKey,
		TestingFromEmail:  fromEmail,
		TestingFromName:   strings.TrimSpace(envconfig.Getenv("MAILJET_FROM_NAME")),
		TestingHttpClient: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func NewPasswordResetSenderFromEnv() (PasswordResetSender, error) {
	return NewSenderFromEnv()
}

func (s *MailjetSender) SendPasswordResetEmail(ctx context.Context, recipientEmail, resetLink string) error {
	if strings.TrimSpace(recipientEmail) == "" {
		return fmt.Errorf("recipient email is required")
	}
	if strings.TrimSpace(resetLink) == "" {
		return fmt.Errorf("reset link is required")
	}

	return s.sendMessage(
		ctx,
		recipientEmail,
		"",
		"Reset your Misty password",
		passwordResetText(resetLink),
		passwordResetHTML(resetLink),
		"password reset",
	)
}

func (s *MailjetSender) SendSpaceInvitationEmail(
	ctx context.Context,
	recipientEmail, inviterName, spaceName, invitationLink string,
) error {
	if strings.TrimSpace(recipientEmail) == "" || strings.TrimSpace(invitationLink) == "" {
		return fmt.Errorf("Space invitation recipient and link are required")
	}
	subject := inviterName + " invited you to " + spaceName + " on Misty"
	textBody := inviterName + " invited you to join " + spaceName + " on Misty.\n\n" +
		"Review and join the Space: " + invitationLink + "\n\nThis invitation expires in 7 days."
	htmlBody := "<p><strong>" + html.EscapeString(inviterName) + "</strong> invited you to join <strong>" +
		html.EscapeString(spaceName) + "</strong> on Misty.</p><p><a href=\"" +
		html.EscapeString(invitationLink) + "\">Review invitation</a></p>" +
		"<p>This invitation expires in 7 days.</p>"
	return s.sendMessage(
		ctx, recipientEmail, "", subject, textBody, htmlBody, "Space invitation",
	)
}
