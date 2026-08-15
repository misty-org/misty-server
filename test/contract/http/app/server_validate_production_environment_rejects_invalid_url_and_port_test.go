package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/app"
)

func TestValidateProductionEnvironmentRejectsInvalidURLAndPort(t *testing.T) {
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
		"MISTY_PUBLIC_API_URL":   "http://api.example.com/api",
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
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	if err := TestingValidateProductionEnvironment(); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("validateProductionEnvironment() error = %v, want https rejection", err)
	}

	t.Setenv("MISTY_PUBLIC_API_URL", "https://api.example.com/api")
	t.Setenv("PORT", "70000")
	if err := TestingValidateProductionEnvironment(); err == nil || !strings.Contains(err.Error(), "PORT") {
		t.Fatalf("validateProductionEnvironment() error = %v, want invalid PORT rejection", err)
	}

	t.Setenv("PORT", "8080")
	t.Setenv("STRIPE_PRICE_MAX_YEARLY", "price_pro_month")
	if err := TestingValidateProductionEnvironment(); err == nil || !strings.Contains(err.Error(), "different Stripe Price IDs") {
		t.Fatalf("validateProductionEnvironment() error = %v, want duplicate Stripe price rejection", err)
	}

	t.Setenv("STRIPE_PRICE_MAX_YEARLY", "price_max_year")
	t.Setenv("STRIPE_PORTAL_RETURN_URL", "http://app.example.com/account")
	if err := TestingValidateProductionEnvironment(); err == nil || !strings.Contains(err.Error(), "STRIPE_PORTAL_RETURN_URL") {
		t.Fatalf("validateProductionEnvironment() error = %v, want insecure Stripe URL rejection", err)
	}
}

func TestCreateServerConfiguresIndependentDevelopmentLibrary(t *testing.T) {
	configureJournalCollabForTest(t)
	t.Setenv("PASSWORD_RESET_URL", "http://localhost:5173/reset")
	t.Setenv("PASSWORD_RESET_START_URL", "http://localhost:8080/auth/reset/start")
	t.Setenv("MAILJET_API_KEY", "")
	t.Setenv("MAILJET_SECRET_KEY", "")
	t.Setenv("MAILJET_FROM_EMAIL", "")
	t.Setenv("R2_ENDPOINT", "")
	t.Setenv("R2_BUCKET", "")
	t.Setenv("R2_ACCESS_KEY", "")
	t.Setenv("R2_SECRET_KEY", "")
	// The memory-backed Library fallback this test exercises is gated on a
	// non-production environment; without this the test's outcome silently
	// depends on whatever MISTY_ENVIRONMENT happens to be set to in the
	// process running the suite (e.g. a local .env that mimics production).
	t.Setenv("MISTY_ENVIRONMENT", "")

	server, err := CreateServer()
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	if server.Library == nil || server.LibraryStore == nil {
		t.Fatal("CreateServer() did not configure the Space Library")
	}
}

func TestValidateProductionEnvironmentAcceptsSelfHostedFilesystemWithoutHostedBilling(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	t.Setenv("MISTY_DEPLOYMENT_MODE", "self_hosted")
	values := map[string]string{
		"DB_HOST": "postgres", "DB_USER": "misty_app", "DB_PASSWORD": "password", "DB_NAME": "misty",
		"MISTY_PUBLIC_API_URL": "https://misty.example.com/api", "MISTY_INSTANCE_NAME": "Studio",
		"MISTY_COLLAB_PUBLIC_URL": "https://misty.example.com",
		"MISTY_LIBRARY_BACKEND":   "filesystem", "MISTY_LIBRARY_FILESYSTEM_DIR": "/var/lib/misty/library",
		"SPACE_LINK_ENCRYPTION_KEY": "01234567890123456789012345678901",
		"PARTYKIT_HOST":             "misty.example.com", "JOURNAL_COLLAB_TICKET_PRIVATE_KEY": "private-key",
		"JOURNAL_COLLAB_CONTROL_SECRET": "control-secret", "JOURNAL_COLLAB_PROJECTION_SECRET": "projection-secret",
		"JOURNAL_COLLAB_ROOM_SALT": "room-salt", "MISTY_COLLAB_INTERNAL_SECRET": "01234567890123456789012345678901",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	if err := TestingValidateProductionEnvironment(); err != nil {
		t.Fatalf("self-hosted production configuration error = %v", err)
	}
}

func TestAllowedCORSOriginsRejectsWildcards(t *testing.T) {
	t.Setenv("MISTY_ALLOWED_ORIGINS", "https://app.misty.example, https://*, http://evil.example/*")
	origins := TestingAllowedCORSOrigins()
	joined := strings.Join(origins, ",")
	if !strings.Contains(joined, "https://app.misty.example") {
		t.Fatalf("configured exact origin missing: %v", origins)
	}
	if strings.Contains(joined, "*") {
		t.Fatalf("wildcard origin was accepted: %v", origins)
	}
}

func TestAllowedCORSOriginAcceptsViteLoopbackPorts(t *testing.T) {
	for _, origin := range []string{"http://localhost:5174", "http://127.0.0.1:5222", "http://[::1]:5175"} {
		if !TestingIsAllowedCORSOrigin(origin) {
			t.Fatalf("isAllowedCORSOrigin(%q) = false, want true", origin)
		}
	}
	for _, origin := range []string{"http://127.0.0.1:5223", "https://127.0.0.1:5174", "http://example.com:5174", "http://127.0.0.1:5174?spoofed=true"} {
		if TestingIsAllowedCORSOrigin(origin) {
			t.Fatalf("isAllowedCORSOrigin(%q) = true, want false", origin)
		}
	}
}

func TestCORSAllowsAppOrigins(t *testing.T) {
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
	if err := server.MountHandlers(); err != nil {
		t.Fatalf("MountHandlers() error = %v", err)
	}

	for _, origin := range []string{"tauri://localhost", "http://127.0.0.1:5174"} {
		req := httptest.NewRequest(http.MethodOptions, "/api/login", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "content-type")
		rec := httptest.NewRecorder()
		server.Router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("OPTIONS /api/login from %s status = %d, want %d", origin, rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("Access-Control-Allow-Origin = %q, want %s", got, origin)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Fatalf("Access-Control-Allow-Credentials = %q, want true", got)
		}
	}

	req := httptest.NewRequest(http.MethodOptions, "/api/spaces", nil)
	req.Header.Set("Origin", "http://127.0.0.1:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type,idempotency-key")
	rec := httptest.NewRecorder()
	server.Router.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:5173" {
		t.Fatalf("Space creation Access-Control-Allow-Origin = %q, want loopback origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(got), "idempotency-key") {
		t.Fatalf("Space creation Access-Control-Allow-Headers = %q, want Idempotency-Key", got)
	}

	req = httptest.NewRequest(http.MethodOptions, "/api/realtime/tickets", nil)
	req.Header.Set("Origin", "http://127.0.0.1:5222")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	rec = httptest.NewRecorder()
	server.Router.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:5222" {
		t.Fatalf("Realtime ticket Access-Control-Allow-Origin = %q, want highest desktop dev port", got)
	}
}
