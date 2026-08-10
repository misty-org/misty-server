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

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

type SubscriptionProperties struct {
	PlanID          string
	Currency        string
	AmountMinor     int64
	Status          string
	BillingInterval string
}

type Client interface {
	UserRegistered(userID, originatingPlatform, releaseChannel string)
	SubscriptionStarted(userID string, properties SubscriptionProperties)
	SubscriptionRenewed(userID string, properties SubscriptionProperties)
	SubscriptionCanceled(userID string, properties SubscriptionProperties)
	Close(context.Context)
}

type NoopClient struct{}

func (NoopClient) UserRegistered(string, string, string)               {}
func (NoopClient) SubscriptionStarted(string, SubscriptionProperties)  {}
func (NoopClient) SubscriptionRenewed(string, SubscriptionProperties)  {}
func (NoopClient) SubscriptionCanceled(string, SubscriptionProperties) {}
func (NoopClient) Close(context.Context)                               {}

type TestingPostHogClient struct {
	TestingToken         string
	TestingHost          string
	TestingEnvironment   string
	TestingServerVersion string
	TestingHttp          *http.Client
	TestingQueue         chan TestingCapture
	TestingDone          chan struct{}
	closeOnce            sync.Once
}

type TestingCapture struct {
	Event, DistinctID string
	Properties        map[string]any
}

func NewFromEnv() Client {
	environment := normalizedEnvironment(envconfig.Getenv("MISTY_ENVIRONMENT"))
	releaseChannel := strings.TrimSpace(envconfig.Getenv("MISTY_RELEASE_CHANNEL"))
	token := strings.TrimSpace(envconfig.Getenv("POSTHOG_PROJECT_TOKEN"))
	host := strings.TrimRight(strings.TrimSpace(envconfig.Getenv("POSTHOG_HOST")), "/")
	if token == "" || host == "" || (environment != "production" && environment != "staging") || !remoteReleaseChannel(releaseChannel) {
		return NoopClient{}
	}
	parsedHost, err := url.ParseRequestURI(host)
	if err != nil || parsedHost.Scheme != "https" || parsedHost.Host == "" {
		return NoopClient{}
	}
	client := &TestingPostHogClient{
		TestingToken: token, TestingHost: host, TestingEnvironment: environment,
		TestingServerVersion: strings.TrimSpace(envconfig.Getenv("MISTY_SERVER_VERSION")),
		TestingHttp:          &http.Client{Timeout: 3 * time.Second}, TestingQueue: make(chan TestingCapture, 256), TestingDone: make(chan struct{}),
	}
	go client.TestingRun()
	return client
}

func (client *TestingPostHogClient) UserRegistered(userID, platform, channel string) {
	properties := map[string]any{"registration_method": "email"}
	if TestingSafePlatform(platform) {
		properties["originating_platform"] = platform
	}
	if remoteReleaseChannel(channel) {
		properties["release_channel"] = channel
	}
	client.enqueue("user_registered", userID, properties)
}

func (client *TestingPostHogClient) SubscriptionStarted(userID string, properties SubscriptionProperties) {
	client.subscription("subscription_started", userID, properties)
}

func (client *TestingPostHogClient) SubscriptionRenewed(userID string, properties SubscriptionProperties) {
	client.subscription("subscription_renewed", userID, properties)
}

func (client *TestingPostHogClient) SubscriptionCanceled(userID string, properties SubscriptionProperties) {
	client.subscription("subscription_canceled", userID, properties)
}

func (client *TestingPostHogClient) subscription(event, userID string, source SubscriptionProperties) {
	properties := map[string]any{
		"provider": "stripe", "plan_id": TestingSafePlan(source.PlanID), "billing_interval": safeInterval(source.BillingInterval),
		"subscription_status": TestingSafeStatus(source.Status),
	}
	if len(source.Currency) == 3 {
		properties["currency"] = strings.ToLower(source.Currency)
	}
	if source.AmountMinor >= 0 {
		properties["amount_minor"] = source.AmountMinor
	}
	client.enqueue(event, userID, properties)
}

func (client *TestingPostHogClient) enqueue(event, userID string, properties map[string]any) {
	if strings.TrimSpace(userID) == "" {
		return
	}
	properties["environment"] = client.TestingEnvironment
	properties["$geoip_disable"] = true
	if client.TestingServerVersion != "" {
		properties["server_version"] = client.TestingServerVersion
	}
	select {
	case client.TestingQueue <- TestingCapture{Event: event, DistinctID: userID, Properties: properties}:
	default:
	}
}

func (client *TestingPostHogClient) TestingRun() {
	defer close(client.TestingDone)
	for item := range client.TestingQueue {
		client.send(item)
	}
}

func (client *TestingPostHogClient) send(item TestingCapture) {
	payload, err := json.Marshal(map[string]any{
		"api_key": client.TestingToken, "event": item.Event,
		"properties": merge(item.Properties, map[string]any{"distinct_id": item.DistinctID}),
		"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return
	}
	request, err := http.NewRequest(http.MethodPost, client.TestingHost+"/i/v0/e/", bytes.NewReader(payload))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.TestingHttp.Do(request)
	if err != nil {
		log.Printf("PostHog delivery failed: %s", sanitizedDeliveryError(err))
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		log.Printf("PostHog delivery failed with status %d", response.StatusCode)
	}
}

func (client *TestingPostHogClient) Close(ctx context.Context) {
	client.closeOnce.Do(func() { close(client.TestingQueue) })
	select {
	case <-client.TestingDone:
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
func TestingSafePlatform(value string) bool {
	switch value {
	case "windows", "macos", "linux", "android", "ios":
		return true
	default:
		return false
	}
}
func TestingSafePlan(value string) string {
	switch value {
	case "personal", "pro", "max":
		return value
	default:
		return "unknown"
	}
}
func safeInterval(value string) string {
	switch value {
	case "monthly", "yearly", "lifetime":
		return value
	default:
		return "other"
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
