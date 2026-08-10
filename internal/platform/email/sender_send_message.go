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
	"strings"
)

func (s *MailjetSender) sendMessage(ctx context.Context, recipientEmail, recipientName, subject, textBody, htmlBody, logLabel string) error {
	payload := TestingMailjetSendRequest{
		Messages: []mailjetMessage{
			{
				From: mailjetContact{
					Email: s.TestingFromEmail,
					Name:  s.TestingFromName,
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.TestingApiBaseURL+"/v3.1/send", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create mailjet %s request: %w", logLabel, err)
	}

	req.SetBasicAuth(s.TestingApiKey, s.TestingSecretKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.TestingHttpClient.Do(req)
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
