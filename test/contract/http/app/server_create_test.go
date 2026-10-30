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
		if got := IsLocalhostHostname(tt.host); got != tt.want {
			t.Fatalf("isLocalhostHostname(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestPasswordResetURLsFromEnv(t *testing.T) {
	t.Setenv("PASSWORD_RESET_URL", "https://app.example.com/reset")
	t.Setenv("PASSWORD_RESET_START_URL", "https://api.example.com/auth/reset/start")

	redirectURL, err := PasswordResetRedirectURLFromEnv()
	if err != nil {
		t.Fatalf("passwordResetRedirectURLFromEnv() error = %v", err)
	}
	if redirectURL != "https://app.example.com/reset" {
		t.Fatalf("redirect URL = %q, want %q", redirectURL, "https://app.example.com/reset")
	}

	startURL, err := PasswordResetStartURLFromEnv()
	if err != nil {
		t.Fatalf("passwordResetStartURLFromEnv() error = %v", err)
	}
	if startURL != "https://api.example.com/auth/reset/start" {
		t.Fatalf("start URL = %q, want %q", startURL, "https://api.example.com/auth/reset/start")
	}
}

func TestPasswordResetURLsRejectNonLocalhostHTTP(t *testing.T) {
	t.Setenv("PASSWORD_RESET_URL", "http://example.com/reset")
	if _, err := PasswordResetRedirectURLFromEnv(); err == nil {
		t.Fatal("passwordResetRedirectURLFromEnv() succeeded for non-localhost http URL")
	}

	t.Setenv("PASSWORD_RESET_START_URL", "http://example.com/auth/reset/start")
	if _, err := PasswordResetStartURLFromEnv(); err == nil {
		t.Fatal("passwordResetStartURLFromEnv() succeeded for non-localhost http URL")
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
	for _, path := range []string{"/stripe/webhook", "/api/stripe/webhook"} {
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
	req.Header.Set("X-Misty-CSRF", "1")
	req.Header.Set("Origin", "tauri://localhost")
	rec := httptest.NewRecorder()
	server.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/login status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateServerRequiresProductionLibraryStorage(t *testing.T) {
	t.Setenv("PASSWORD_RESET_URL", "http://localhost:5173/reset")
	t.Setenv("PASSWORD_RESET_START_URL", "http://localhost:8080/auth/reset/start")
	t.Setenv("MISTY_ENVIRONMENT", "production")
	for key, value := range productionCoreValues() {
		t.Setenv(key, value)
	}
	for _, key := range []string{"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY", "R2_SECRET_KEY", "MISTY_S3_ENDPOINT", "MISTY_S3_BUCKET", "MISTY_S3_ACCESS_KEY_ID", "MISTY_S3_SECRET_ACCESS_KEY", "MISTY_LIBRARY_BACKEND", "MISTY_LIBRARY_FILESYSTEM_DIR", "MISTY_LIBRARY_LOCAL_DIR"} {
		t.Setenv(key, "")
	}

	if _, err := CreateServer(); err == nil || !strings.Contains(err.Error(), "MISTY_LIBRARY_FILESYSTEM_DIR") {
		t.Fatalf("CreateServer() error = %v, want missing production Library storage rejection", err)
	}
}

func productionCoreValues() map[string]string {
	return map[string]string{
		"DB_HOST":                   "database.internal",
		"DB_USER":                   "misty",
		"DB_PASSWORD":               "password",
		"DB_NAME":                   "misty",
		"MISTY_PUBLIC_API_URL":      "https://api.example.com/api",
		"SPACE_LINK_ENCRYPTION_KEY": "01234567890123456789012345678901",
		"PORT":                      "8080",
	}
}

func TestValidateProductionEnvironmentAcceptsFilesystemLibrary(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	for key, value := range productionCoreValues() {
		t.Setenv(key, value)
	}
	for _, key := range []string{"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY", "R2_SECRET_KEY", "MISTY_S3_ENDPOINT", "MISTY_LIBRARY_BACKEND"} {
		t.Setenv(key, "")
	}
	t.Setenv("MISTY_LIBRARY_FILESYSTEM_DIR", t.TempDir())
	if err := ValidateProductionEnvironment(); err != nil {
		t.Fatalf("self-hosted production with a Library directory = %v", err)
	}
	store, err := LibraryStoreFromEnv()
	if err != nil || store == nil {
		t.Fatalf("filesystem Library store in production = %v", err)
	}
}

func TestValidateProductionEnvironmentRequiresEveryS3Key(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	for key, value := range productionCoreValues() {
		t.Setenv(key, value)
	}
	t.Setenv("MISTY_LIBRARY_FILESYSTEM_DIR", "")
	t.Setenv("MISTY_LIBRARY_BACKEND", "s3")
	t.Setenv("R2_ENDPOINT", "https://account.r2.cloudflarestorage.com")
	t.Setenv("R2_BUCKET", "misty-production")
	t.Setenv("R2_ACCESS_KEY", "")
	t.Setenv("R2_SECRET_KEY", "secret")
	if err := ValidateProductionEnvironment(); err == nil || !strings.Contains(err.Error(), "R2_ACCESS_KEY") {
		t.Fatalf("S3 storage without its access key = %v", err)
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

	if err := ValidateProductionEnvironment(); err == nil || !strings.Contains(err.Error(), "DB_HOST") {
		t.Fatalf("validateProductionEnvironment() error = %v, want first missing variable", err)
	}
}

func TestValidateProductionEnvironmentAcceptsCoreConfiguration(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	values := map[string]string{
		"R2_ENDPOINT":               "https://account.r2.cloudflarestorage.com",
		"R2_BUCKET":                 "misty-production",
		"R2_ACCESS_KEY":             "access",
		"R2_SECRET_KEY":             "secret",
		"DB_HOST":                   "database.internal",
		"DB_USER":                   "misty",
		"DB_PASSWORD":               "password",
		"DB_NAME":                   "misty",
		"MISTY_PUBLIC_API_URL":      "https://api.example.com/api",
		"SPACE_LINK_ENCRYPTION_KEY": "01234567890123456789012345678901",
		"PORT": "8080",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}

	if err := ValidateProductionEnvironment(); err != nil {
		t.Fatalf("validateProductionEnvironment() error = %v", err)
	}
}
