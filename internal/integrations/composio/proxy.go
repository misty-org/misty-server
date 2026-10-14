package composio

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ProxyResult is the app's own answer to a proxied request.
type ProxyResult struct {
	Status int
	Data   json.RawMessage
}

// Proxy sends one request to a connected account's app API (for GitHub,
// api.github.com) with Composio adding the account's credentials, which never
// reach Misty. The endpoint is a path on that API. Any app status is returned
// as a result; only a failure to get an answer is an error.
func (c *Client) Proxy(ctx context.Context, account, method, endpoint string, body any) (ProxyResult, error) {
	if !accountPattern.MatchString(account) || !strings.HasPrefix(endpoint, "/") || strings.Contains(endpoint, "://") {
		return ProxyResult{}, &APIError{Status: http.StatusBadRequest, Message: "invalid proxied request"}
	}
	return c.proxy(ctx, account, method, endpoint, body)
}

// proxyHosts are the app APIs Misty calls by absolute URL, for apps whose
// Composio base path isn't the API root.
var proxyHosts = map[string]bool{"gmail.googleapis.com": true, "slack.com": true}

// ProxyURL is Proxy for a full https URL on one of the allowed app APIs.
func (c *Client) ProxyURL(ctx context.Context, account, method, address string, body any) (ProxyResult, error) {
	parsed, err := url.Parse(address)
	if err != nil || !accountPattern.MatchString(account) || parsed.Scheme != "https" || parsed.User != nil || !proxyHosts[parsed.Host] {
		return ProxyResult{}, &APIError{Status: http.StatusBadRequest, Message: "invalid proxied request"}
	}
	return c.proxy(ctx, account, method, address, body)
}

func (c *Client) proxy(ctx context.Context, account, method, endpoint string, body any) (ProxyResult, error) {
	payload := map[string]any{"endpoint": endpoint, "method": method, "connected_account_id": account}
	if body != nil {
		payload["body"] = body
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ProxyResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"tools/execute/proxy", bytes.NewReader(data))
	if err != nil {
		return ProxyResult{}, err
	}
	request.Header.Set("x-api-key", c.key)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return ProxyResult{}, ErrTransport
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return ProxyResult{}, ErrTransport
	}
	if len(raw) > maxResponseBytes {
		return ProxyResult{}, &APIError{Status: http.StatusBadGateway, Message: "The app returned more data than Misty accepts."}
	}
	var envelope struct {
		Status int             `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) == nil && envelope.Status != 0 {
		return ProxyResult{Status: envelope.Status, Data: envelope.Data}, nil
	}
	if response.StatusCode >= 500 {
		return ProxyResult{}, apiError(response.StatusCode, raw)
	}
	// Composio passes some app errors through as its own status.
	return ProxyResult{Status: response.StatusCode, Data: raw}, nil
}
