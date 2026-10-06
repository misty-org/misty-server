package composio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrTransport means a request got no response, so its effect is unknown.
var ErrTransport = errors.New("Composio did not respond")

const maxResponseBytes = 4 << 20

// APIError is an answer Composio returned. Message is safe to show the model.
type APIError struct {
	Status    int
	Message   string
	RequestID string
}

func (e *APIError) Error() string {
	if e.RequestID != "" {
		return fmt.Sprintf("Composio HTTP %d: %s (request %s)", e.Status, e.Message, e.RequestID)
	}
	return fmt.Sprintf("Composio HTTP %d: %s", e.Status, e.Message)
}

// Rejected reports whether Composio answered without running the request.
// Timeouts and server errors are not rejections: the request may have run.
func Rejected(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 500 && apiErr.Status != http.StatusRequestTimeout
}

// NotFound reports a missing session, account or tool.
func NotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

// Client calls Composio's v3.1 REST API with the project key. Redirects are
// refused so the key never follows one.
type Client struct {
	base string
	key  string
	http *http.Client
}

func New(c Config) (*Client, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &Client{base: CloudEndpoint + "/api/v3.1/", key: c.APIKey, http: &http.Client{
		Timeout:       45 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	target := c.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}
	request.Header.Set("x-api-key", c.key)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return errors.Join(ErrTransport, ctx.Err())
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return ErrTransport
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return apiError(response.StatusCode, raw)
	}
	if len(raw) > maxResponseBytes {
		return &APIError{Status: http.StatusBadGateway, Message: "The app returned more data than Misty accepts. Narrow the request."}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &APIError{Status: http.StatusBadGateway, Message: "Composio returned an unreadable response."}
	}
	return nil
}

func apiError(status int, raw []byte) *APIError {
	var envelope struct {
		Error struct {
			Message      string `json:"message"`
			RequestID    string `json:"request_id"`
			SuggestedFix string `json:"suggested_fix"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &envelope)
	message := strings.TrimSpace(envelope.Error.Message + " " + envelope.Error.SuggestedFix)
	if message == "" {
		message = fmt.Sprintf("Composio returned HTTP %d.", status)
	}
	return &APIError{Status: status, Message: truncate(message, 400), RequestID: truncate(envelope.Error.RequestID, 80)}
}

// truncate cuts at a rune boundary at or before limit bytes.
func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	cut := 0
	for index := range value {
		if index > limit {
			break
		}
		cut = index
	}
	return strings.TrimSpace(value[:cut]) + "…"
}
