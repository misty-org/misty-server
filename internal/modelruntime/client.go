// Package modelruntime is the Go API's only way to call AI models. The agent
// runtime makes every call with the Vercel AI SDK on Misty's own keys; Go
// chooses the model, meters the call, and sends it here signed with the shared
// runtime control secret. Go holds no model HTTP clients of its own.
package modelruntime

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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

	"github.com/kannachi323/misty/server/internal/aimodels"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const responseLimit = 16 << 20

// ErrUnavailable means no agent runtime is configured, so no model can run.
var ErrUnavailable = errors.New("the AI runtime is not configured")

type Client struct {
	url    string
	secret []byte
	http   *http.Client
}

// FromEnv reads the runtime address and secret the agent runtime already uses.
// Outside production a missing runtime is allowed; every call then fails with
// ErrUnavailable.
func FromEnv() (*Client, error) {
	rawURL := strings.TrimRight(strings.TrimSpace(envconfig.Getenv("MISTY_AGENT_RUNTIME_URL")), "/")
	rawSecret := strings.TrimSpace(envconfig.Getenv("MISTY_AGENT_RUNTIME_CONTROL_SECRET"))
	if rawURL == "" && rawSecret == "" && !strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_ENVIRONMENT")), "production") {
		return &Client{}, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("MISTY_AGENT_RUNTIME_URL must be an absolute URL")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "agent-runtime" {
		return nil, errors.New("MISTY_AGENT_RUNTIME_URL must use HTTPS except for the local runtime")
	}
	secret, err := base64.StdEncoding.DecodeString(rawSecret)
	if err != nil || len(secret) < 32 {
		return nil, errors.New("MISTY_AGENT_RUNTIME_CONTROL_SECRET must be at least 32 base64-encoded random bytes")
	}
	return New(strings.TrimRight(parsed.String(), "/"), secret, nil), nil
}

// New builds a client for a known runtime; tests pass their own HTTP client.
func New(baseURL string, secret []byte, client *http.Client) *Client {
	if client == nil {
		// Library batches with images and long audio can take a while.
		client = &http.Client{Timeout: 3 * time.Minute}
	}
	// A redirect would resend a signed call to another address, so redirects
	// are failures, never followed.
	noRedirects := *client
	noRedirects.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{url: strings.TrimRight(baseURL, "/"), secret: secret, http: &noRedirects}
}

func (c *Client) Enabled() bool { return c != nil && c.url != "" && len(c.secret) > 0 }

// Sign is the runtime control signature: method, path, timestamp and body
// digest under the shared secret.
func Sign(secret []byte, method, path, timestamp string, body []byte) string {
	digest := sha256.Sum256(body)
	message := strings.Join([]string{strings.ToUpper(method), path, timestamp, hex.EncodeToString(digest[:])}, "\n")
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

// Route is how a call runs. It is always Misty's own: the runtime sends OpenAI
// models to OpenAI when the instance has a key, and everything else through
// the AI Gateway. Only the reasoning an account chose travels with it.
type Route struct {
	Provider  string `json:"provider"`
	Reasoning string `json:"reasoning,omitempty"`
}

// Instance runs on Misty's own keys with the model's default reasoning.
func Instance() Route { return Route{Provider: "instance"} }

// For carries an account's chosen reasoning, if it has one.
func For(resolved *aimodels.Resolved) Route {
	if resolved == nil {
		return Instance()
	}
	return Route{Provider: "instance", Reasoning: resolved.Reasoning}
}

type Part struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	Data      string `json:"data,omitempty"`
}

func Text(text string) Part { return Part{Type: "text", Text: text} }

func Image(mediaType string, data []byte) Part {
	return Part{Type: "image", MediaType: mediaType, Data: base64.StdEncoding.EncodeToString(data)}
}

type Message struct {
	Role    string `json:"role"`
	Content []Part `json:"content"`
}

type Schema struct {
	Name   string `json:"name"`
	Schema any    `json:"schema"`
}

type TextRequest struct {
	Route           Route     `json:"route"`
	Model           string    `json:"model"`
	System          string    `json:"system,omitempty"`
	Messages        []Message `json:"messages"`
	MaxOutputTokens int64     `json:"maxOutputTokens"`
	// Reasoning is the call's own; a route's reasoning, when set, wins.
	Reasoning string  `json:"reasoning,omitempty"`
	Schema    *Schema `json:"schema,omitempty"`
}

type Usage struct {
	InputTokens       int64 `json:"inputTokens"`
	CachedInputTokens int64 `json:"cachedInputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	ReasoningTokens   int64 `json:"reasoningTokens"`
}

type TextResult struct {
	Text         string          `json:"text"`
	Object       json.RawMessage `json:"object"`
	Usage        Usage           `json:"usage"`
	FinishReason string          `json:"finishReason"`
}

func (c *Client) Text(ctx context.Context, request TextRequest) (TextResult, error) {
	var result TextResult
	err := c.post(ctx, "/v1/models/text", request, &result)
	return result, err
}

type EmbedImage struct {
	MediaType string `json:"mediaType"`
	Data      string `json:"data"`
}

type EmbedRequest struct {
	Route      Route    `json:"route"`
	Model      string   `json:"model"`
	Values     []string `json:"values"`
	Dimensions int      `json:"dimensions,omitempty"`
	// Images, when set, holds one optional image per value; each is embedded
	// together with its value in the same vector.
	Images []*EmbedImage `json:"images,omitempty"`
}

type EmbedResult struct {
	Embeddings [][]float64 `json:"embeddings"`
	Usage      Usage       `json:"usage"`
}

func (c *Client) Embed(ctx context.Context, request EmbedRequest) (EmbedResult, error) {
	var result EmbedResult
	err := c.post(ctx, "/v1/models/embed", request, &result)
	return result, err
}

type TranscribeRequest struct {
	Route     Route  `json:"route"`
	Model     string `json:"model"`
	MediaType string `json:"mediaType"`
	Audio     string `json:"audio"`
}

type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type TranscribeResult struct {
	Text              string    `json:"text"`
	Language          string    `json:"language"`
	DurationInSeconds *float64  `json:"durationInSeconds"`
	Segments          []Segment `json:"segments"`
}

func (c *Client) Transcribe(ctx context.Context, route Route, model, mediaType string, audio []byte) (TranscribeResult, error) {
	var result TranscribeResult
	err := c.post(ctx, "/v1/models/transcribe", TranscribeRequest{
		Route: route, Model: model, MediaType: mediaType, Audio: base64.StdEncoding.EncodeToString(audio),
	}, &result)
	return result, err
}

// Error is a failed model call. It never contains a provider response body.
type Error struct {
	Status         int
	Code           string
	Message        string
	UpstreamStatus int
}

func (e *Error) Error() string {
	if e.UpstreamStatus > 0 {
		return fmt.Sprintf("model call failed: %s (provider status %d)", e.Code, e.UpstreamStatus)
	}
	return fmt.Sprintf("model call failed: %s (status %d)", e.Code, e.Status)
}

// Temporary reports whether retrying the same call may succeed.
func (e *Error) Temporary() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500 && (e.UpstreamStatus == 0 || e.UpstreamStatus == http.StatusTooManyRequests || e.UpstreamStatus >= 500)
}

func (c *Client) post(ctx context.Context, path string, body, output any) error {
	if !c.Enabled() {
		return ErrUnavailable
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Misty-Agent-Timestamp", timestamp)
	request.Header.Set("X-Misty-Agent-Signature", Sign(c.secret, http.MethodPost, path, timestamp, payload))
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return err
	}
	if len(raw) > responseLimit {
		return errors.New("model call response too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		failure := &Error{Status: response.StatusCode, Code: "model_call_failed"}
		var decoded struct {
			Code           string `json:"code"`
			Message        string `json:"message"`
			UpstreamStatus int    `json:"upstream_status"`
		}
		if json.Unmarshal(raw, &decoded) == nil && decoded.Code != "" {
			failure.Code, failure.Message, failure.UpstreamStatus = decoded.Code, decoded.Message, decoded.UpstreamStatus
		}
		return failure
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return fmt.Errorf("model call returned invalid JSON: %w", err)
	}
	return nil
}
