package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

const (
	workflowHTTPMaxRequest  = 1 << 20
	workflowHTTPMaxResponse = 1 << 20
)

// executeOutboundHTTPNode is the sole generic network escape hatch in the v2
// runtime. It can only call public HTTPS addresses and it never installs an
// inbound callback. Authentication is resolved from a user-owned connection;
// inline Authorization, Cookie, and proxy headers are rejected.
func (s *SpacesService) executeOutboundHTTPNode(ctx context.Context, run *db.SpaceRun, invocation workflowv2.Invocation) (json.RawMessage, error) {
	var config struct {
		URL          string            `json:"url"`
		Method       string            `json:"method"`
		Headers      map[string]string `json:"headers"`
		Query        map[string]string `json:"query"`
		Body         json.RawMessage   `json:"body"`
		Timeout      int               `json:"timeoutSeconds"`
		ConnectionID string            `json:"connectionId"`
	}
	if json.Unmarshal(invocation.Config, &config) != nil {
		return nil, workflowv2.ErrOutputInvalid
	}
	input := extractWorkflowText(invocation.Input)
	rawURL := strings.ReplaceAll(strings.TrimSpace(config.URL), "{{input}}", url.QueryEscape(input))
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("HTTP request nodes require a public HTTPS URL")
	}
	query := parsed.Query()
	for key, value := range config.Query {
		if key == "" || strings.ContainsAny(key, "\r\n") {
			return nil, workflowv2.ErrOutputInvalid
		}
		query.Set(key, strings.ReplaceAll(value, "{{input}}", input))
	}
	parsed.RawQuery = query.Encode()
	method := strings.ToUpper(strings.TrimSpace(config.Method))
	if method == "" {
		method = http.MethodGet
	}
	if !map[string]bool{http.MethodGet: true, http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true}[method] {
		return nil, errors.New("HTTP request method is not allowed")
	}
	body := config.Body
	if len(body) == 0 || string(body) == "null" {
		body = invocation.Input
	}
	body = bytes.ReplaceAll(body, []byte("{{input}}"), []byte(input))
	if len(body) > workflowHTTPMaxRequest {
		return nil, errors.New("HTTP request payload exceeded 1 MiB")
	}
	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json, text/plain;q=0.9")
	if method != http.MethodGet && len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range config.Headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(key))
		switch canonical {
		case "Authorization", "Cookie", "Proxy-Authorization", "Host", "Connection", "Upgrade":
			return nil, errors.New("sensitive HTTP headers must come from a connection")
		}
		if canonical == "" || strings.ContainsAny(key+value, "\r\n") {
			return nil, workflowv2.ErrOutputInvalid
		}
		request.Header.Set(canonical, strings.ReplaceAll(value, "{{input}}", input))
	}
	if config.ConnectionID != "" {
		token, tokenType, err := s.providerAccessToken(ctx, run.RequestingMemberID, run.SpaceID, config.ConnectionID)
		if err != nil {
			return nil, err
		}
		if tokenType == "" {
			tokenType = "Bearer"
		}
		request.Header.Set("Authorization", tokenType+" "+token)
	}
	timeout := config.Timeout
	if timeout == 0 {
		timeout = 15
	}
	if timeout < 1 || timeout > 30 {
		return nil, errors.New("HTTP request timeout must be between 1 and 30 seconds")
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
			host, port, splitErr := net.SplitHostPort(address)
			if splitErr != nil {
				return nil, splitErr
			}
			ips, resolveErr := net.DefaultResolver.LookupIPAddr(dialCtx, host)
			if resolveErr != nil {
				return nil, resolveErr
			}
			for _, candidate := range ips {
				if TestingIsPublicWorkflowIP(candidate.IP) {
					return (&net.Dialer{Timeout: 8 * time.Second}).DialContext(dialCtx, network, net.JoinHostPort(candidate.IP.String(), port))
				}
			}
			return nil, errors.New("HTTP request target resolves to a private or reserved address")
		},
		TLSHandshakeTimeout: 8 * time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: time.Duration(timeout) * time.Second, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 3 || next.URL.Scheme != "https" || next.URL.User != nil {
			return errors.New("HTTP request redirect was rejected")
		}
		// A redirect never carries credentials to another authority.
		if len(via) > 0 && !strings.EqualFold(next.URL.Host, via[0].URL.Host) {
			next.Header.Del("Authorization")
			next.Header.Del("Cookie")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, workflowHTTPMaxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > workflowHTTPMaxResponse {
		return nil, errors.New("HTTP response exceeded 1 MiB")
	}
	result := map[string]any{
		"status":      response.StatusCode,
		"ok":          response.StatusCode >= 200 && response.StatusCode < 300,
		"contentType": response.Header.Get("Content-Type"),
		"body":        string(payload),
		"bytes":       len(payload),
		"attempt":     invocation.Attempt,
	}
	if retryAfter := response.Header.Get("Retry-After"); retryAfter != "" {
		if seconds, parseErr := strconv.Atoi(retryAfter); parseErr == nil {
			result["retryAfterSeconds"] = seconds
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return TestingMustAPIRawJSON(result), fmt.Errorf("HTTP request returned %s", response.Status)
	}
	return TestingMustAPIRawJSON(result), nil
}
