package app

import (
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	. "github.com/kannachi323/misty/server/internal/app"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

func TestRouteInventory(t *testing.T) {
	configureJournalCollabForTest(t)
	t.Setenv("MISTY_ENVIRONMENT", "")
	t.Setenv("STRIPE_WEBHOOK_PATH", "")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "")
	t.Setenv("MISTY_DEVICE_JOBS_ENABLED", "false")
	t.Setenv("MISTY_METRICS_TOKEN", "")
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

	var routes []string
	if err := chi.Walk(server.Router, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, method+" "+path)
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	sort.Strings(routes)
	actual := strings.Join(routes, "\n") + "\n"

	const inventoryPath = "routes.golden"
	if envconfig.Getenv("UPDATE_ROUTE_GOLDEN") == "1" {
		if err := os.WriteFile(inventoryPath, []byte(actual), 0o644); err != nil {
			t.Fatalf("update route inventory: %v", err)
		}
	}
	expected, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatalf("read route inventory: %v", err)
	}
	if actual != string(expected) {
		t.Fatalf("route inventory changed; review the HTTP compatibility impact and run UPDATE_ROUTE_GOLDEN=1 go test ./test/contract/http/app -run TestRouteInventory")
	}
}
