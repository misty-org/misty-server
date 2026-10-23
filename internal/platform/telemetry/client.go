package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/config"
)

type Client interface {
	UserRegistered(userID, originatingPlatform, releaseChannel string)
	Close(context.Context)
}

type NoopClient struct{}

func (NoopClient) UserRegistered(string, string, string) {}
func (NoopClient) Close(context.Context)                 {}

type PostHogClient struct {
	Token         string
	Host          string
	Environment   string
	ServerVersion string
	HTTP          *http.Client
	Queue         chan Capture
	Done          chan struct{}
	closeOnce     sync.Once
}

type Capture struct {
	Event, DistinctID string
	Properties        map[string]any
}

func NewFromEnv() Client {
	environment := normalizedEnvironment(config.Getenv("MISTY_ENVIRONMENT"))
	releaseChannel := strings.TrimSpace(config.Getenv("MISTY_RELEASE_CHANNEL"))
	token := strings.TrimSpace(config.Getenv("POSTHOG_PROJECT_TOKEN"))
	host := strings.TrimRight(strings.TrimSpace(config.Getenv("POSTHOG_HOST")), "/")
	if token == "" || host == "" || (environment != "production" && environment != "staging") || !remoteReleaseChannel(releaseChannel) {
		return NoopClient{}
	}
	parsedHost, err := url.ParseRequestURI(host)
	if err != nil || parsedHost.Scheme != "https" || parsedHost.Host == "" {
		return NoopClient{}
	}
	client := &PostHogClient{
		Token: token, Host: host, Environment: environment,
		ServerVersion: strings.TrimSpace(config.Getenv("MISTY_SERVER_VERSION")),
		HTTP:          &http.Client{Timeout: 3 * time.Second}, Queue: make(chan Capture, 256), Done: make(chan struct{}),
	}
	go client.Run()
	return client
}

func (client *PostHogClient) UserRegistered(userID, platform, channel string) {
	properties := map[string]any{"registration_method": "email"}
	if SafePlatform(platform) {
		properties["originating_platform"] = platform
	}
	if remoteReleaseChannel(channel) {
		properties["release_channel"] = channel
	}
	client.enqueue("user_registered", userID, properties)
}

func (client *PostHogClient) enqueue(event, userID string, properties map[string]any) {
	if strings.TrimSpace(userID) == "" {
		return
	}
	properties["environment"] = client.Environment
	properties["$geoip_disable"] = true
	if client.ServerVersion != "" {
		properties["server_version"] = client.ServerVersion
	}
	select {
	case client.Queue <- Capture{Event: event, DistinctID: userID, Properties: properties}:
	default:
	}
}

func (client *PostHogClient) Run() {
	defer close(client.Done)
	for item := range client.Queue {
		client.send(item)
	}
}

func (client *PostHogClient) send(item Capture) {
	payload, err := json.Marshal(map[string]any{
		"api_key": client.Token, "event": item.Event,
		"properties": merge(item.Properties, map[string]any{"distinct_id": item.DistinctID}),
		"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return
	}
	request, err := http.NewRequest(http.MethodPost, client.Host+"/i/v0/e/", bytes.NewReader(payload))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.HTTP.Do(request)
	if err != nil {
		log.Printf("PostHog delivery failed: %s", sanitizedDeliveryError(err))
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		log.Printf("PostHog delivery failed with status %d", response.StatusCode)
	}
}

func (client *PostHogClient) Close(ctx context.Context) {
	client.closeOnce.Do(func() { close(client.Queue) })
	select {
	case <-client.Done:
	case <-ctx.Done():
	}
}

func merge(left, right map[string]any) map[string]any {
	result := make(map[string]any, len(left)+len(right))
	for key, value := range left {
		result[key] = value
	}
	for key, value := range right {
		result[key] = value
	}
	return result
}
func normalizedEnvironment(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "production":
		return "production"
	case "staging":
		return "staging"
	case "test":
		return "test"
	default:
		return "development"
	}
}
func remoteReleaseChannel(value string) bool {
	switch strings.TrimSpace(value) {
	case "internal", "private_alpha", "private_beta", "public_beta", "production":
		return true
	default:
		return false
	}
}
func SafePlatform(value string) bool {
	switch value {
	case "windows", "macos", "linux":
		return true
	default:
		return false
	}
}
func TestingSafePlan(value string) string {
	switch value {
	case "basic", "pro", "max", "ultra", "ultra_plus":
		return value
	default:
		return "unknown"
	}
}
func TestingSafeStatus(value string) string {
	switch value {
	case "trialing", "active", "past_due", "canceled", "expired":
		return value
	default:
		return "canceled"
	}
}
func sanitizedDeliveryError(_ error) string { return "network request failed" }
