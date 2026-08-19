package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const (
	agentRuntimeBodyLimit = 2 << 20
	agentRuntimeMaxSkew   = 5 * time.Minute
)

type AgentRuntimeConfig struct {
	Kind           string
	URL            string
	InternalAPIURL string
	secret         []byte
	previousSecret []byte
	client         *http.Client
}

func AgentRuntimeConfigFromEnv() (AgentRuntimeConfig, error) {
	config := AgentRuntimeConfig{Kind: "vercel-workflow", client: &http.Client{Timeout: 20 * time.Second}}
	rawURL := strings.TrimRight(strings.TrimSpace(envconfig.Getenv("MISTY_AGENT_RUNTIME_URL")), "/")
	rawSecret := strings.TrimSpace(envconfig.Getenv("MISTY_AGENT_RUNTIME_CONTROL_SECRET"))
	if rawURL == "" && rawSecret == "" && !strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_ENVIRONMENT")), "production") {
		return config, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return AgentRuntimeConfig{}, errors.New("MISTY_AGENT_RUNTIME_URL must be an absolute URL")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "agent-runtime" {
		return AgentRuntimeConfig{}, errors.New("MISTY_AGENT_RUNTIME_URL must use HTTPS except for the local runtime")
	}
	config.URL = strings.TrimRight(parsed.String(), "/")
	config.InternalAPIURL = strings.TrimRight(strings.TrimSpace(envconfig.Getenv("MISTY_AGENT_RUNTIME_INTERNAL_API_URL")), "/")
	if config.InternalAPIURL == "" {
		config.InternalAPIURL = "http://api:8080"
	}
	if config.secret, err = decodeServiceSecret("MISTY_AGENT_RUNTIME_CONTROL_SECRET"); err != nil {
		return AgentRuntimeConfig{}, err
	}
	if config.previousSecret, err = decodeOptionalServiceSecret("MISTY_AGENT_RUNTIME_CONTROL_SECRET_PREVIOUS"); err != nil {
		return AgentRuntimeConfig{}, err
	}
	return config, nil
}

func (c AgentRuntimeConfig) Enabled() bool {
	return c.URL != "" && len(c.secret) > 0
}

type agentRuntimeStartRequest struct {
	RunID       string `json:"run_id"`
	CallbackURL string `json:"callback_url"`
}

type agentRuntimeStartResponse struct {
	RuntimeRunID string `json:"runtime_run_id"`
}

func (c AgentRuntimeConfig) Start(ctx context.Context, runID string) (string, error) {
	body, _ := json.Marshal(agentRuntimeStartRequest{RunID: runID, CallbackURL: c.InternalAPIURL})
	var response agentRuntimeStartResponse
	if err := c.request(ctx, http.MethodPost, "/v1/runs", runID, body, &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.RuntimeRunID) == "" {
		return "", errors.New("agent runtime returned no run id")
	}
	return response.RuntimeRunID, nil
}

func (c AgentRuntimeConfig) Cancel(ctx context.Context, runtimeRunID, mistyRunID string) error {
	if runtimeRunID == "" {
		return nil
	}
	path := "/v1/runs/" + url.PathEscape(runtimeRunID) + "/cancel"
	return c.request(ctx, http.MethodPost, path, mistyRunID+":cancel", []byte(`{}`), nil)
}

func (c AgentRuntimeConfig) ResumeApproval(ctx context.Context, hookToken, runID, approvalID string, approved bool) error {
	body, _ := json.Marshal(map[string]any{"approved": approved, "approval_id": approvalID})
	path := "/v1/approvals/" + url.PathEscape(hookToken)
	return c.request(ctx, http.MethodPost, path, runID+":approval:"+approvalID, body, nil)
}

func (c AgentRuntimeConfig) ResumeDevice(ctx context.Context, hookToken, runID string, available bool) error {
	body, _ := json.Marshal(map[string]any{"available": available})
	path := "/v1/devices/" + url.PathEscape(hookToken)
	return c.request(ctx, http.MethodPost, path, runID+":device:"+strconv.FormatBool(available), body, nil)
}

func (c AgentRuntimeConfig) request(ctx context.Context, method, path, idempotencyKey string, body []byte, output any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Misty-Agent-Timestamp", timestamp)
	req.Header.Set("X-Misty-Agent-Signature", signAgentRuntimeRequest(c.secret, method, path, timestamp, body))
	req.Header.Set("Idempotency-Key", idempotencyKey)
	response, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, agentRuntimeBodyLimit+1))
	if err != nil {
		return err
	}
	if len(responseBody) > agentRuntimeBodyLimit {
		return errors.New("agent runtime response too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("agent runtime returned %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if output != nil && len(responseBody) > 0 {
		return json.Unmarshal(responseBody, output)
	}
	return nil
}

func signAgentRuntimeRequest(secret []byte, method, path, timestamp string, body []byte) string {
	digest := sha256.Sum256(body)
	message := strings.Join([]string{strings.ToUpper(method), path, timestamp, hex.EncodeToString(digest[:])}, "\n")
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func (c AgentRuntimeConfig) verifyRequest(r *http.Request, body []byte) bool {
	timestamp := strings.TrimSpace(r.Header.Get("X-Misty-Agent-Timestamp"))
	signature := strings.TrimSpace(r.Header.Get("X-Misty-Agent-Signature"))
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || signature == "" || time.Since(time.Unix(seconds, 0)).Abs() > agentRuntimeMaxSkew {
		return false
	}
	path := r.URL.EscapedPath()
	for _, secret := range [][]byte{c.secret, c.previousSecret} {
		if len(secret) == 0 {
			continue
		}
		expected, err := hex.DecodeString(signAgentRuntimeRequest(secret, r.Method, path, timestamp, body))
		provided, decodeErr := hex.DecodeString(signature)
		if err == nil && decodeErr == nil && hmac.Equal(expected, provided) {
			return true
		}
	}
	return false
}

func readAgentRuntimeRequest(c AgentRuntimeConfig, w http.ResponseWriter, r *http.Request, output any) bool {
	if !c.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "agent_runtime_disabled"})
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, agentRuntimeBodyLimit+1))
	if err != nil || len(body) > agentRuntimeBodyLimit || !c.verifyRequest(r, body) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "agent_runtime_unauthorized"})
		return false
	}
	if r.Method != http.MethodGet && strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "idempotency_key_required"})
		return false
	}
	if output != nil && len(body) > 0 && json.Unmarshal(body, output) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_json"})
		return false
	}
	return true
}

// TestingAgentRuntimeSignature exposes the wire signature for tests that live
// outside production packages by repository convention.
func TestingAgentRuntimeSignature(secret []byte, method, path, timestamp string, body []byte) string {
	return signAgentRuntimeRequest(secret, method, path, timestamp, body)
}

func TestingAgentRuntimeSignatureVerifies(current, previous []byte, method, path, timestamp, signature string, body []byte) bool {
	request := &http.Request{Method: method, URL: &url.URL{Path: path}, Header: http.Header{}}
	request.Header.Set("X-Misty-Agent-Timestamp", timestamp)
	request.Header.Set("X-Misty-Agent-Signature", signature)
	return (AgentRuntimeConfig{secret: current, previousSecret: previous}).verifyRequest(request, body)
}
