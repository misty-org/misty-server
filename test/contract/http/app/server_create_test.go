package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/misty-org/misty-server/internal/app"
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
		if got := IsLocalhostHostname(tt.host); got != tt.want {
			t.Fatalf("isLocalhostHostname(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestStripeWebhookIsNotMounted(t *testing.T) {
	configureJournalCollabForTest(t)
	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err = server.MountHandlers(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/stripe/webhook", "/v1/stripe/webhook"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		server.Router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("retired route %s: %d", path, rec.Code)
		}
	}
}

func TestCreateServerAndMountHandlers(t *testing.T) {
	configureJournalCollabForTest(t)

	server, err := CreateServer()
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	if server.Router == nil || server.Database == nil {
		t.Fatalf("server not fully initialized: %#v", server)
	}

	if err := server.MountHandlers(); err != nil {
		t.Fatalf("MountHandlers() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/login", nil)
	req.Header.Set("X-Misty-CSRF", "1")
	req.Header.Set("Origin", "tauri://localhost")
	rec := httptest.NewRecorder()
	server.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /v1/login status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateServerRequiresProductionLibraryStorage(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	for key, value := range productionCoreValues() {
		t.Setenv(key, value)
	}
	for _, key := range []string{"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY", "R2_SECRET_KEY"} {
		t.Setenv(key, "")
	}

	if _, err := CreateServer(); err == nil || !strings.Contains(err.Error(), "R2_ENDPOINT") {
		t.Fatalf("CreateServer() error = %v, want missing production Library storage rejection", err)
	}
	if _, err := LibraryStoreFromEnv(); err == nil {
		t.Fatal("production opened a Library store without R2")
	}
}

// productionCoreValues is a complete production configuration.
func productionCoreValues() map[string]string {
	values := map[string]string{
		"DB_HOST":              "database.internal",
		"DB_USER":              "misty",
		"DB_PASSWORD":          "password",
		"DB_NAME":              "misty",
		"MISTY_PUBLIC_API_URL": "https://api.example.com/v1",
		"R2_ENDPOINT":          "https://account.r2.cloudflarestorage.com",
		"R2_BUCKET":            "misty-production",
		"PORT":                 "8080",
	}
	for _, name := range []string{
		"SPACE_LINK_ENCRYPTION_KEY", "MISTY_AUTH_SIGNING_KEY",
		"R2_ACCESS_KEY", "R2_SECRET_KEY",
		"PARTYKIT_HOST", "JOURNAL_COLLAB_TICKET_PRIVATE_KEY", "JOURNAL_COLLAB_CONTROL_SECRET",
		"JOURNAL_COLLAB_PROJECTION_SECRET", "JOURNAL_COLLAB_ROOM_SALT",
		"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET",
	} {
		values[name] = "configured"
	}
	return values
}

func TestValidateProductionEnvironmentRequiresEveryIntegration(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	for key, value := range productionCoreValues() {
		t.Setenv(key, value)
	}
	if err := ValidateProductionEnvironment(); err != nil {
		t.Fatalf("a complete production configuration = %v", err)
	}
	for _, name := range []string{"R2_ACCESS_KEY", "PARTYKIT_HOST", "GOOGLE_CLIENT_ID"} {
		t.Setenv(name, "")
		if err := ValidateProductionEnvironment(); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("production without %s = %v", name, err)
		}
		t.Setenv(name, productionCoreValues()[name])
	}
}

func TestProductionRefusesServerModelKeys(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	for key, value := range productionCoreValues() {
		t.Setenv(key, value)
	}
	for _, name := range []string{"OPENAI_API_KEY", "AI_GATEWAY_API_KEY", "ELEVENLABS_API_KEY", "MISTY_AGENT_MODEL"} {
		t.Setenv(name, "configured")
		if err := ValidateProductionEnvironment(); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("production with %s = %v", name, err)
		}
		t.Setenv(name, "")
	}
}

func TestValidateProductionEnvironmentRequiresCoreConfiguration(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	for key := range productionCoreValues() {
		t.Setenv(key, "")
	}

	if err := ValidateProductionEnvironment(); err == nil || !strings.Contains(err.Error(), "DB_HOST") {
		t.Fatalf("validateProductionEnvironment() error = %v, want first missing variable", err)
	}
}

func TestValidateProductionEnvironmentRequiresHTTPS(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	for key, value := range productionCoreValues() {
		t.Setenv(key, value)
	}
	t.Setenv("MISTY_PUBLIC_API_URL", "http://api.example.com/v1")
	if err := ValidateProductionEnvironment(); err == nil {
		t.Fatal("production accepted an http public URL")
	}
}

// The server answers only under /v1, so a public URL without it would send
// Google's sign-in callback and sign-in links to paths that answer 404.
func TestValidateProductionEnvironmentRequiresV1PublicURL(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	for key, value := range productionCoreValues() {
		t.Setenv(key, value)
	}
	for _, value := range []string{"https://api.example.com", "https://api.example.com/", "https://api.example.com/api", "https://api.example.com/v1/extra"} {
		t.Setenv("MISTY_PUBLIC_API_URL", value)
		if err := ValidateProductionEnvironment(); err == nil || !strings.Contains(err.Error(), "/v1") {
			t.Fatalf("production with MISTY_PUBLIC_API_URL=%s = %v", value, err)
		}
	}
	for _, value := range []string{"https://api.example.com/v1", "https://api.example.com/v1/", "https://example.com/misty/v1"} {
		t.Setenv("MISTY_PUBLIC_API_URL", value)
		if err := ValidateProductionEnvironment(); err != nil {
			t.Fatalf("production with MISTY_PUBLIC_API_URL=%s = %v", value, err)
		}
	}
}
