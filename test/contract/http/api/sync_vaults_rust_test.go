package api

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/browsersync"
	"github.com/misty-org/misty-server/internal/platform/security"
)

// The real desktop client against the real handlers, socket and database: two sessions, real key
// derivation, and everything in between (crates/browser-sync/tests/vault_live.rs). This is what
// would have caught a client and server that each passed their own tests but disagreed. Set
// MISTY_VAULT_LIVE_CLIENT to the desktop crate's Cargo.toml to run it.
func TestRustClientAgainstRealServer(t *testing.T) {
	manifest := os.Getenv("MISTY_VAULT_LIVE_CLIENT")
	if manifest == "" {
		t.Skip("set MISTY_VAULT_LIVE_CLIENT to the browser-sync crate's Cargo.toml")
	}
	database := openPresenceTestDatabase(t)
	service := browsersync.NewBrowserSyncService(database)
	defer service.Shutdown()
	router := chi.NewRouter()
	router.Route("/v1/sync", func(r chi.Router) {
		r.Get("/vault", service.Vault())
		r.Post("/vault", service.Vault())
		r.Delete("/vault", service.Vault())
		r.Put("/vault/key", service.VaultKey())
		r.Get("/sessions", service.Sessions())
		r.Post("/sessions", service.Sessions())
		r.Put("/sessions/{sessionID}/name", service.SessionName())
		r.Post("/sessions/{sessionID}/remove", service.SessionRemove())
		r.Get("/usage", service.Usage())
		r.Post("/ticket", service.Ticket())
		r.Get("/ws", service.Connect())
	})
	server := httptest.NewServer(router)
	defer server.Close()
	user, _ := syncTestUser(t, database)
	// The run takes longer than the usual test token lasts, and nothing here can refresh one.
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Mint(user, uniqueTestEmail("session"), "access", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("cargo", "test", "--manifest-path", manifest, "--test", "vault_live", "--", "--nocapture")
	command.Env = append(os.Environ(), "MISTY_VAULT_LIVE_URL="+server.URL+"/v1", "MISTY_VAULT_LIVE_TOKEN="+token)
	output, err := command.CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatalf("the client's live test failed: %v", err)
	}
}
