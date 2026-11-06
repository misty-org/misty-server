package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	envconfig "github.com/misty-org/misty-server/internal/platform/config"
	"github.com/misty-org/misty-server/internal/platform/db"
)

const (
	agentRuntimeBodyLimit = 2 << 20
	agentRuntimeMaxSkew   = 5 * time.Minute
)

var errAgentRuntimeStartUnconfirmed = errors.New("workflow_start_unconfirmed")

// AgentRuntimeConfig holds the server secret that signs run keys and MCP
// bearers. Runs always work on the person's desktop, which claims them as
// deliveries (agent_runtime_device.go, docs/agents/runtime.md).
type AgentRuntimeConfig struct {
	database       *db.Database
	Kind           string
	secret         []byte
	previousSecret []byte
}

func AgentRuntimeConfigFromEnv() (AgentRuntimeConfig, error) {
	config := AgentRuntimeConfig{Kind: "vercel-workflow"}
	rawSecret := strings.TrimSpace(envconfig.Getenv("MISTY_AGENT_RUNTIME_CONTROL_SECRET"))
	if rawSecret == "" && !strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_ENVIRONMENT")), "production") {
		return config, nil
	}
	var err error
	if config.secret, err = decodeServiceSecret("MISTY_AGENT_RUNTIME_CONTROL_SECRET"); err != nil {
		return AgentRuntimeConfig{}, err
	}
	if config.previousSecret, err = decodeOptionalServiceSecret("MISTY_AGENT_RUNTIME_CONTROL_SECRET_PREVIOUS"); err != nil {
		return AgentRuntimeConfig{}, err
	}
	return config, nil
}

// Enabled reports whether agents can run: the server holds the secret that signs
// run keys and MCP bearers.
func (c AgentRuntimeConfig) Enabled() bool {
	return len(c.secret) > 0
}

// Start queues a run for the desktop that keeps it; its runtime records the
// run ID when it starts the run.
func (c AgentRuntimeConfig) Start(ctx context.Context, runID string) (string, error) {
	if c.database == nil {
		return "", errAgentRuntimeOnDevice
	}
	return "", c.database.QueueAgentRuntimeStart(ctx, runID)
}

// Cancel queues a cancel for the desktop running the run.
func (c AgentRuntimeConfig) Cancel(ctx context.Context, runtimeRunID, mistyRunID string) error {
	if runtimeRunID == "" {
		return nil
	}
	if c.database == nil {
		return errAgentRuntimeOnDevice
	}
	return c.database.QueueAgentRuntimeCancel(ctx, mistyRunID, runtimeRunID)
}

// signAgentRuntimeRequest is the runtime control signature: method, path,
// timestamp and body digest under a run's key.
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
	// A desktop runtime signs with the run's own key (agent_runtime_device.go).
	// The server secret itself never leaves the server, so nothing signs with it.
	runID := chi.URLParam(r, "runID")
	if runID == "" {
		return false
	}
	for _, secret := range [][]byte{agentRunKey(c.secret, runID), agentRunKey(c.previousSecret, runID)} {
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

// TestingAgentRunKey is the key a desktop runtime signs runID's calls with.
func TestingAgentRunKey(secret []byte, runID string) []byte { return agentRunKey(secret, runID) }

func TestingAgentRuntimeSignatureVerifies(current, previous []byte, runID, method, path, timestamp, signature string, body []byte) bool {
	request := &http.Request{Method: method, URL: &url.URL{Path: path}, Header: http.Header{}}
	request.Header.Set("X-Misty-Agent-Timestamp", timestamp)
	request.Header.Set("X-Misty-Agent-Signature", signature)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("runID", runID)
	request = request.WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, routeContext))
	return (AgentRuntimeConfig{secret: current, previousSecret: previous}).verifyRequest(request, body)
}
