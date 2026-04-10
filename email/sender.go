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

// PasswordResetSender is intentionally provider-agnostic so SMTP, SES, or Mailjet API senders can implement it.
type PasswordResetSender interface {
	SendPasswordResetEmail(ctx context.Context, email, resetLink string) error
}

// LogPasswordResetSender is a safe default for local development until a real sender is wired in.
type LogPasswordResetSender struct{}

func (LogPasswordResetSender) SendPasswordResetEmail(_ context.Context, email, resetLink string) error {
	log.Printf("password reset email to %s: %s", email, resetLink)
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

func NewPasswordResetSenderFromEnv() (PasswordResetSender, error) {
	apiKey := strings.TrimSpace(os.Getenv("MAILJET_API_KEY"))
	secretKey := strings.TrimSpace(os.Getenv("MAILJET_SECRET_KEY"))
	fromEmail := strings.TrimSpace(os.Getenv("MAILJET_FROM_EMAIL"))

	if apiKey == "" && secretKey == "" && fromEmail == "" {
		return LogPasswordResetSender{}, nil
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

func (s *MailjetSender) SendPasswordResetEmail(ctx context.Context, recipientEmail, resetLink string) error {
	if strings.TrimSpace(recipientEmail) == "" {
		return fmt.Errorf("recipient email is required")
	}
	if strings.TrimSpace(resetLink) == "" {
		return fmt.Errorf("reset link is required")
	}

	payload := mailjetSendRequest{
		Messages: []mailjetMessage{
			{
				From: mailjetContact{
					Email: s.fromEmail,
					Name:  s.fromName,
				},
				To: []mailjetContact{
					{Email: recipientEmail},
				},
				Subject:  "Reset your Misty password",
				TextPart: passwordResetText(resetLink),
				HTMLPart: passwordResetHTML(resetLink),
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal mailjet password reset request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiBaseURL+"/v3.1/send", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create mailjet password reset request: %w", err)
	}

	req.SetBasicAuth(s.apiKey, s.secretKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send password reset email via mailjet: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("read mailjet send response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("mailjet send failed with status %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}

	var sendResponse mailjetSendResponse
	if err := json.Unmarshal(responseBody, &sendResponse); err != nil {
		return fmt.Errorf("parse mailjet send response: %w", err)
	}
	if len(sendResponse.Messages) == 0 {
		return fmt.Errorf("mailjet send returned no messages: %s", strings.TrimSpace(string(responseBody)))
	}

	result := sendResponse.Messages[0]
	if !strings.EqualFold(result.Status, "success") {
		return fmt.Errorf("mailjet message status %q: %s", result.Status, formatMailjetErrors(result.Errors))
	}

	log.Printf("mailjet accepted password reset email for %s with status=%s", recipientEmail, result.Status)
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
