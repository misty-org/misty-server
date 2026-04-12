package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultMailjetAPIBaseURL = "https://api.mailjet.com"

type PasswordResetSender interface {
	SendPasswordResetEmail(ctx context.Context, recipientEmail, resetLink string) error
}

type WaitlistSender interface {
	SendWaitlistConfirmationEmail(ctx context.Context, recipientName, recipientEmail string) error
	SendWaitlistNotificationEmail(ctx context.Context, notifyEmail, waitlistName, waitlistEmail string) error
}

type Sender interface {
	PasswordResetSender
	WaitlistSender
}

type LogSender struct{}

func (LogSender) SendPasswordResetEmail(_ context.Context, recipientEmail, resetLink string) error {
	log.Printf("password reset email to %s: %s", recipientEmail, resetLink)
	return nil
}

func (LogSender) SendWaitlistConfirmationEmail(_ context.Context, recipientName, recipientEmail string) error {
	log.Printf("waitlist confirmation email to %s (%s)", recipientName, recipientEmail)
	return nil
}

func (LogSender) SendWaitlistNotificationEmail(_ context.Context, notifyEmail, waitlistName, waitlistEmail string) error {
	log.Printf("waitlist notification email to %s about %s (%s)", notifyEmail, waitlistName, waitlistEmail)
	return nil
}

type MailjetSender struct {
	apiBaseURL string
	apiKey     string
	secretKey  string
	fromEmail  string
	fromName   string
	httpClient *http.Client
}

type mailjetSendRequest struct {
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
	apiKey := strings.TrimSpace(os.Getenv("MAILJET_API_KEY"))
	secretKey := strings.TrimSpace(os.Getenv("MAILJET_SECRET_KEY"))
	fromEmail := strings.TrimSpace(os.Getenv("MAILJET_FROM_EMAIL"))

	if apiKey == "" && secretKey == "" && fromEmail == "" {
		return LogSender{}, nil
	}
	if apiKey == "" || secretKey == "" || fromEmail == "" {
		return nil, fmt.Errorf("MAILJET_API_KEY, MAILJET_SECRET_KEY, and MAILJET_FROM_EMAIL must all be set")
	}

	apiBaseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("MAILJET_API_BASE_URL")), "/")
	if apiBaseURL == "" {
		apiBaseURL = defaultMailjetAPIBaseURL
	}

	return &MailjetSender{
		apiBaseURL: apiBaseURL,
		apiKey:     apiKey,
		secretKey:  secretKey,
		fromEmail:  fromEmail,
		fromName:   strings.TrimSpace(os.Getenv("MAILJET_FROM_NAME")),
		httpClient: &http.Client{Timeout: 10 * time.Second},
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

func (s *MailjetSender) SendWaitlistConfirmationEmail(ctx context.Context, recipientName, recipientEmail string) error {
	if strings.TrimSpace(recipientEmail) == "" {
		return fmt.Errorf("recipient email is required")
	}

	return s.sendMessage(
		ctx,
		recipientEmail,
		recipientName,
		"You're on the Misty waitlist",
		waitlistConfirmationText(recipientName),
		waitlistConfirmationHTML(recipientName),
		"waitlist confirmation",
	)
}

func (s *MailjetSender) SendWaitlistNotificationEmail(ctx context.Context, notifyEmail, waitlistName, waitlistEmail string) error {
	if strings.TrimSpace(notifyEmail) == "" {
		return nil
	}
	if strings.TrimSpace(waitlistEmail) == "" {
		return fmt.Errorf("waitlist email is required")
	}

	return s.sendMessage(
		ctx,
		notifyEmail,
		"",
		"New Misty waitlist signup",
		waitlistNotificationText(waitlistName, waitlistEmail),
		waitlistNotificationHTML(waitlistName, waitlistEmail),
		"waitlist notification",
	)
}

func (s *MailjetSender) sendMessage(ctx context.Context, recipientEmail, recipientName, subject, textBody, htmlBody, logLabel string) error {
	payload := mailjetSendRequest{
		Messages: []mailjetMessage{
			{
				From: mailjetContact{
					Email: s.fromEmail,
					Name:  s.fromName,
				},
				To: []mailjetContact{
					{
						Email: recipientEmail,
						Name:  strings.TrimSpace(recipientName),
					},
				},
				Subject:  subject,
				TextPart: textBody,
				HTMLPart: htmlBody,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal mailjet %s request: %w", logLabel, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiBaseURL+"/v3.1/send", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create mailjet %s request: %w", logLabel, err)
	}

	req.SetBasicAuth(s.apiKey, s.secretKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send %s email via mailjet: %w", logLabel, err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("read mailjet %s response: %w", logLabel, err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("mailjet %s failed with status %s: %s", logLabel, resp.Status, strings.TrimSpace(string(responseBody)))
	}

	var sendResponse mailjetSendResponse
	if err := json.Unmarshal(responseBody, &sendResponse); err != nil {
		return fmt.Errorf("parse mailjet %s response: %w", logLabel, err)
	}
	if len(sendResponse.Messages) == 0 {
		return fmt.Errorf("mailjet %s returned no messages: %s", logLabel, strings.TrimSpace(string(responseBody)))
	}

	result := sendResponse.Messages[0]
	if !strings.EqualFold(result.Status, "success") {
		return fmt.Errorf("mailjet %s status %q: %s", logLabel, result.Status, formatMailjetErrors(result.Errors))
	}

	log.Printf("mailjet accepted %s email for %s with status=%s", logLabel, recipientEmail, result.Status)
	return nil
}

func formatMailjetErrors(errors []struct {
	ErrorIdentifier string `json:"ErrorIdentifier"`
	ErrorCode       string `json:"ErrorCode"`
	StatusCode      int    `json:"StatusCode"`
	ErrorMessage    string `json:"ErrorMessage"`
}) string {
	if len(errors) == 0 {
		return "no additional details"
	}

	var parts []string
	for _, item := range errors {
		piece := strings.TrimSpace(item.ErrorMessage)
		if item.ErrorCode != "" {
			piece = strings.TrimSpace(item.ErrorCode + ": " + piece)
		}
		if item.StatusCode != 0 {
			piece = fmt.Sprintf("%d %s", item.StatusCode, piece)
		}
		if piece == "" {
			piece = "unknown mailjet error"
		}
		parts = append(parts, piece)
	}

	return strings.Join(parts, "; ")
}

func passwordResetText(resetLink string) string {
	return fmt.Sprintf(
		"Use the link below to reset your Misty password. This link expires in 15 minutes.\n\n%s\n",
		resetLink,
	)
}

func passwordResetHTML(resetLink string) string {
	escapedLink := html.EscapeString(resetLink)
	return fmt.Sprintf(
		"<p>Use the link below to reset your Misty password. This link expires in 15 minutes.</p><p><a href=\"%s\">Reset your password</a></p><p>If the button does not work, copy and paste this URL into your browser:</p><p>%s</p>",
		escapedLink,
		escapedLink,
	)
}

func waitlistConfirmationText(name string) string {
	greeting := "Hi"
	if trimmedName := strings.TrimSpace(name); trimmedName != "" {
		greeting = "Hi " + trimmedName
	}

	return fmt.Sprintf(
		"%s,\n\nYou're on the Misty waitlist.\n\nWe'll email you when we're ready to open things up.\n",
		greeting,
	)
}

func waitlistConfirmationHTML(name string) string {
	greeting := "Hi"
	if trimmedName := strings.TrimSpace(name); trimmedName != "" {
		greeting = "Hi " + html.EscapeString(trimmedName)
	}

	return fmt.Sprintf(
		"<p>%s,</p><p>You're on the Misty waitlist.</p><p>We'll email you when we're ready to open things up.</p>",
		greeting,
	)
}

func waitlistNotificationText(name, email string) string {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		trimmedName = "(no name provided)"
	}

	return fmt.Sprintf(
		"New waitlist signup\n\nName: %s\nEmail: %s\n",
		trimmedName,
		strings.TrimSpace(email),
	)
}

func waitlistNotificationHTML(name, email string) string {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		trimmedName = "(no name provided)"
	}

	return fmt.Sprintf(
		"<p>New waitlist signup</p><p><strong>Name:</strong> %s<br><strong>Email:</strong> %s</p>",
		html.EscapeString(trimmedName),
		html.EscapeString(strings.TrimSpace(email)),
	)
}
