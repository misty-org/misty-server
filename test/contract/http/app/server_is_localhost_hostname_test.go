package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/app"
)

func TestIsLocalhostHostname(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{host: "localhost", want: true},
		{host: "127.0.0.1", want: true},
		{host: "::1", want: true},
		{host: " example.com ", want: false},
	}

	for _, tt := range tests {
		if got := TestingIsLocalhostHostname(tt.host); got != tt.want {
			t.Fatalf("isLocalhostHostname(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestPasswordResetURLsFromEnv(t *testing.T) {
	t.Setenv("PASSWORD_RESET_URL", "https://app.example.com/reset")
	t.Setenv("PASSWORD_RESET_START_URL", "https://api.example.com/auth/reset/start")

	redirectURL, err := TestingPasswordResetRedirectURLFromEnv()
	if err != nil {
		t.Fatalf("passwordResetRedirectURLFromEnv() error = %v", err)
	}
	if redirectURL != "https://app.example.com/reset" {
		t.Fatalf("redirect URL = %q, want %q", redirectURL, "https://app.example.com/reset")
	}

	startURL, err := TestingPasswordResetStartURLFromEnv()
	if err != nil {
		t.Fatalf("passwordResetStartURLFromEnv() error = %v", err)
	}
	if startURL != "https://api.example.com/auth/reset/start" {
		t.Fatalf("start URL = %q, want %q", startURL, "https://api.example.com/auth/reset/start")
	}
}

func TestPasswordResetURLsRejectNonLocalhostHTTP(t *testing.T) {
	t.Setenv("PASSWORD_RESET_URL", "http://example.com/reset")
	if _, err := TestingPasswordResetRedirectURLFromEnv(); err == nil {
		t.Fatal("passwordResetRedirectURLFromEnv() succeeded for non-localhost http URL")
	}

	t.Setenv("PASSWORD_RESET_START_URL", "http://example.com/auth/reset/start")
	if _, err := TestingPasswordResetStartURLFromEnv(); err == nil {
		t.Fatal("passwordResetStartURLFromEnv() succeeded for non-localhost http URL")
	}
}

func TestStripeWebhookPathFromEnv(t *testing.T) {
	t.Setenv("STRIPE_WEBHOOK_PATH", "")
	path, err := TestingStripeWebhookPathFromEnv()
	if err != nil {
		t.Fatalf("stripeWebhookPathFromEnv() default error = %v", err)
	}
	if path != TestingDefaultStripeWebhookPath {
		t.Fatalf("default webhook path = %q, want %q", path, TestingDefaultStripeWebhookPath)
	}

	t.Setenv("STRIPE_WEBHOOK_PATH", "/dev/stripe/events")
	path, err = TestingStripeWebhookPathFromEnv()
	if err != nil {
		t.Fatalf("stripeWebhookPathFromEnv() custom error = %v", err)
	}
	if path != "/dev/stripe/events" {
		t.Fatalf("custom webhook path = %q, want %q", path, "/dev/stripe/events")
	}

	for _, invalid := range []string{
		"stripe/webhook",
		"http://localhost:8081/stripe/webhook",
		"//stripe/webhook",
		"/stripe/webhook?source=dev",
		"/stripe/{event}",
		"/",
	} {
		t.Setenv("STRIPE_WEBHOOK_PATH", invalid)
		if _, err := TestingStripeWebhookPathFromEnv(); err == nil {
			t.Fatalf("stripeWebhookPathFromEnv() accepted invalid path %q", invalid)
		}
	}
}

func TestMountHandlersUsesConfiguredStripeWebhookPath(t *testing.T) {
	configureJournalCollabForTest(t)
	t.Setenv("MISTY_ENVIRONMENT", "")
	t.Setenv("PASSWORD_RESET_URL", "http://localhost:5173/reset")
	t.Setenv("PASSWORD_RESET_START_URL", "http://localhost:8081/auth/reset/start")
	t.Setenv("STRIPE_WEBHOOK_PATH", "/dev/stripe/webhook")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "")
	t.Setenv("R2_ENDPOINT", "")
	t.Setenv("R2_BUCKET", "")
	t.Setenv("R2_ACCESS_KEY", "")
	t.Setenv("R2_SECRET_KEY", "")

	server, err := CreateServer()
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	if err := server.MountHandlers(); err != nil {
		t.Fatalf("MountHandlers() error = %v", err)
	}

	configuredRequest := httptest.NewRequest(http.MethodPost, "/dev/stripe/webhook", strings.NewReader("{}"))
	configuredResponse := httptest.NewRecorder()
	server.Router.ServeHTTP(configuredResponse, configuredRequest)
	if configuredResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("configured webhook status = %d, want %d", configuredResponse.Code, http.StatusServiceUnavailable)
	}

	defaultRequest := httptest.NewRequest(http.MethodPost, TestingDefaultStripeWebhookPath, strings.NewReader("{}"))
	defaultResponse := httptest.NewRecorder()
	server.Router.ServeHTTP(defaultResponse, defaultRequest)
	if defaultResponse.Code != http.StatusNotFound {
		t.Fatalf("default webhook status = %d, want %d", defaultResponse.Code, http.StatusNotFound)
	}
}

func TestCreateServerAndMountHandlers(t *testing.T) {
	configureJournalCollabForTest(t)
	t.Setenv("PASSWORD_RESET_URL", "http://localhost:5173/reset")
	t.Setenv("PASSWORD_RESET_START_URL", "http://localhost:8080/auth/reset/start")
	t.Setenv("MAILJET_API_KEY", "")
	t.Setenv("MAILJET_SECRET_KEY", "")
	t.Setenv("MAILJET_FROM_EMAIL", "")

	server, err := CreateServer()
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	if server.Router == nil || server.Database == nil || server.EmailSender == nil {
		t.Fatalf("server not fully initialized: %#v", server)
	}

	if err := server.MountHandlers(); err != nil {
		t.Fatalf("MountHandlers() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	rec := httptest.NewRecorder()
	server.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/login status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateServerRequiresProductionR2Configuration(t *testing.T) {
	t.Setenv("PASSWORD_RESET_URL", "http://localhost:5173/reset")
	t.Setenv("PASSWORD_RESET_START_URL", "http://localhost:8080/auth/reset/start")
	t.Setenv("MISTY_ENVIRONMENT", "production")
	t.Setenv("R2_ENDPOINT", "")
	t.Setenv("R2_BUCKET", "")
	t.Setenv("R2_ACCESS_KEY", "")
	t.Setenv("R2_SECRET_KEY", "")

	if _, err := CreateServer(); err == nil || !strings.Contains(err.Error(), "R2_ENDPOINT") {
		t.Fatalf("CreateServer() error = %v, want missing production R2 rejection", err)
	}
}

func TestValidateProductionEnvironmentRequiresCoreConfiguration(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	for _, key := range []string{
		"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY", "R2_SECRET_KEY",
		"DB_HOST", "DB_USER", "DB_PASSWORD", "DB_NAME",
		"MISTY_PUBLIC_API_URL", "SPACE_LINK_ENCRYPTION_KEY",
	} {
		t.Setenv(key, "")
	}

	if err := TestingValidateProductionEnvironment(); err == nil || !strings.Contains(err.Error(), "R2_ENDPOINT") {
		t.Fatalf("validateProductionEnvironment() error = %v, want first missing variable", err)
	}
}

func TestValidateProductionEnvironmentAcceptsCoreConfiguration(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	values := map[string]string{
		"R2_ENDPOINT":            "https://account.r2.cloudflarestorage.com",
		"R2_BUCKET":              "misty-production",
		"R2_ACCESS_KEY":          "access",
		"R2_SECRET_KEY":          "secret",
		"DB_HOST":                "database.internal",
		"DB_USER":                "misty",
		"DB_PASSWORD":            "password",
		"DB_NAME":                "misty",
		"MISTY_PUBLIC_API_URL":   "https://api.example.com/api",
		"MISTY_OPERATOR_USER_ID": "operator-user-id",
		"MISTY_SELF_HOST_ENTITLEMENT_PRIVATE_KEY":    "private-key",
		"MISTY_SELF_HOST_ENTITLEMENT_KEY_ID":         "misty-2026-01",
		"MISTY_SELF_HOST_ENTITLEMENT_SUBJECT_SECRET": "subject-secret",
		"SPACE_LINK_ENCRYPTION_KEY":                  "01234567890123456789012345678901",
		"STRIPE_SECRET_KEY":                          "sk_live_test",
		"STRIPE_WEBHOOK_SECRET":                      "whsec_01234567890123456789",
		"STRIPE_PRICE_PRO_MONTHLY":                   "price_pro_month",
		"STRIPE_PRICE_PRO_YEARLY":                    "price_pro_year",
		"STRIPE_PRICE_MAX_MONTHLY":                   "price_max_month",
		"STRIPE_PRICE_MAX_YEARLY":                    "price_max_year",
		"STRIPE_CHECKOUT_SUCCESS_URL":                "https://app.example.com/billing/success",
		"STRIPE_CHECKOUT_CANCEL_URL":                 "https://app.example.com/billing/cancel",
		"STRIPE_PORTAL_RETURN_URL":                   "https://app.example.com/account",
		"PORT":                                       "8080",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}

	if err := TestingValidateProductionEnvironment(); err != nil {
		t.Fatalf("validateProductionEnvironment() error = %v", err)
	}
}
