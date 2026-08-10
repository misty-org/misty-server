package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/telemetry"
)

func TestMissingConfigurationAndTestsUseNoop(t *testing.T) {
	t.Setenv("POSTHOG_PROJECT_TOKEN", "")
	t.Setenv("POSTHOG_HOST", "")
	if _, ok := NewFromEnv().(NoopClient); !ok {
		t.Fatal("expected telemetry to be disabled")
	}
	t.Setenv("POSTHOG_PROJECT_TOKEN", "phc_test")
	t.Setenv("POSTHOG_HOST", "https://us.i.posthog.com")
	t.Setenv("MISTY_ENVIRONMENT", "test")
	t.Setenv("MISTY_RELEASE_CHANNEL", "production")
	client := NewFromEnv()
	if _, ok := client.(NoopClient); !ok {
		t.Fatal("test environment must not create a remote client")
	}
	client.Close(context.Background())
}

func TestCaptureUsesOnlySafeServerProperties(t *testing.T) {
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		received <- payload
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &TestingPostHogClient{
		TestingToken: "phc_test", TestingHost: server.URL, TestingEnvironment: "production", TestingServerVersion: "1.0.0",
		TestingHttp: server.Client(), TestingQueue: make(chan TestingCapture, 4), TestingDone: make(chan struct{}),
	}
	go client.TestingRun()
	client.UserRegistered("opaque-user-id", "alice@example.com", "production")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client.Close(ctx)

	select {
	case payload := <-received:
		encoded, _ := json.Marshal(payload)
		if string(encoded) == "" || containsSensitive(string(encoded)) {
			t.Fatalf("unsafe payload: %s", encoded)
		}
	case <-time.After(time.Second):
		t.Fatal("capture was not delivered")
	}
}

func containsSensitive(value string) bool {
	for _, sensitive := range []string{"alice@example.com", "password", "access_token", "refresh_token"} {
		if strings.Contains(value, sensitive) {
			return true
		}
	}
	return false
}

func TestServerPropertiesAreAllowlisted(t *testing.T) {
	if TestingSafePlan("/Users/alice/secret") != "unknown" {
		t.Fatal("unsafe plan was retained")
	}
	if TestingSafeStatus("card_number") != "canceled" {
		t.Fatal("unsafe status was retained")
	}
	if TestingSafePlatform("alice@example.com") {
		t.Fatal("unsafe platform was retained")
	}
}
