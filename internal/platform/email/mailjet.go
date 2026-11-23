package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/misty-org/misty-server/internal/platform/config"
)

const DefaultBaseURL = "https://api.mailjet.com"

type Message struct {
	To       string
	Subject  string
	Text     string
	HTML     string
	CustomID string
}

type Sender interface {
	Send(ctx context.Context, m Message) error
}

type Client struct {
	APIKey, SecretKey   string
	FromEmail, FromName string
	BaseURL             string
	HTTP                *http.Client
}

func FromEnv() (*Client, error) {
	key := strings.TrimSpace(config.Getenv("MAILJET_API_KEY"))
	secret := strings.TrimSpace(config.Getenv("MAILJET_SECRET_KEY"))
	from := strings.TrimSpace(config.Getenv("MAILJET_FROM_EMAIL"))
	if key == "" && secret == "" && from == "" {
		return nil, nil
	}
	if key == "" || secret == "" || from == "" {
		return nil, errors.New("MAILJET_API_KEY, MAILJET_SECRET_KEY and MAILJET_FROM_EMAIL must all be set to send email")
	}
	if address, err := mail.ParseAddress(from); err != nil || address.Address != from {
		return nil, errors.New("MAILJET_FROM_EMAIL must be a plain email address, such as hello@example.com")
	}
	name := strings.TrimSpace(config.Getenv("MAILJET_FROM_NAME"))
	if name == "" {
		name = "Misty"
	}
	base := strings.TrimRight(strings.TrimSpace(config.Getenv("MAILJET_API_BASE_URL")), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname()))) {
		return nil, errors.New("MAILJET_API_BASE_URL must be an https URL (http only on loopback)")
	}
	return &Client{APIKey: key, SecretKey: secret, FromEmail: from, FromName: name, BaseURL: base, HTTP: &http.Client{Timeout: 15 * time.Second}}, nil
}

func Configured() bool {
	client, err := FromEnv()
	return err == nil && client != nil
}

func loopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	text := fmt.Sprintf("mailjet answered %d", e.Status)
	if e.Code != "" {
		text += " " + e.Code
	}
	if e.Message != "" {
		text += ": " + e.Message
	}
	return text
}

type address struct {
	Email string `json:"Email"`
	Name  string `json:"Name,omitempty"`
}

type message struct {
	From     address   `json:"From"`
	To       []address `json:"To"`
	Subject  string    `json:"Subject"`
	TextPart string    `json:"TextPart"`
	HTMLPart string    `json:"HTMLPart,omitempty"`
	CustomID string    `json:"CustomID,omitempty"`
}

type answer struct {
	Messages []struct {
		Status string `json:"Status"`
		Errors []struct {
			ErrorCode    string `json:"ErrorCode"`
			ErrorMessage string `json:"ErrorMessage"`
		} `json:"Errors"`
	} `json:"Messages"`
	ErrorIdentifier string `json:"ErrorIdentifier"`
	ErrorMessage    string `json:"ErrorMessage"`
}

func (c *Client) Send(ctx context.Context, m Message) error {
	if strings.TrimSpace(m.To) == "" || m.Subject == "" || m.Text == "" {
		return errors.New("mailjet: a message needs a recipient, a subject and a text part")
	}
	raw, err := json.Marshal(map[string]any{"Messages": []message{{
		From: address{Email: c.FromEmail, Name: c.FromName}, To: []address{{Email: strings.TrimSpace(m.To)}},
		Subject: m.Subject, TextPart: m.Text, HTMLPart: m.HTML, CustomID: m.CustomID,
	}}})
	if err != nil {
		return err
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v3.1/send", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.APIKey, c.SecretKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("mailjet unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var got answer
	decoded := json.Unmarshal(body, &got) == nil
	if resp.StatusCode/100 == 2 && decoded && len(got.Messages) == 1 && got.Messages[0].Status == "success" {
		return nil
	}
	refusal := &Error{Status: resp.StatusCode, Code: got.ErrorIdentifier, Message: got.ErrorMessage}
	for _, sent := range got.Messages {
		if len(sent.Errors) > 0 {
			refusal.Code, refusal.Message = sent.Errors[0].ErrorCode, sent.Errors[0].ErrorMessage
			break
		}
	}
	if refusal.Message == "" && resp.StatusCode/100 == 2 {
		refusal.Message = "the answer did not confirm the message"
	}
	if len(refusal.Message) > 200 {
		refusal.Message = refusal.Message[:200]
	}
	return refusal
}
