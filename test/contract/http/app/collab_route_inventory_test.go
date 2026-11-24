package app

import (
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/platform/config"
	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func TestCollabRouteInventory(t *testing.T) {
	router := chi.NewRouter()
	(&api.CollabTransports{}).Mount(router)
	var routes []string
	if err := chi.Walk(router, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, method+" "+path)
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	sort.Strings(routes)
	actual := strings.Join(routes, "\n") + "\n"
	const inventoryPath = "collab_routes.golden"
	if config.Getenv("UPDATE_ROUTE_GOLDEN") == "1" {
		if err := os.WriteFile(inventoryPath, []byte(actual), 0o644); err != nil {
			t.Fatalf("update route inventory: %v", err)
		}
	}
	expected, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatalf("read route inventory: %v", err)
	}
	if actual != string(expected) {
		t.Fatalf("collab route inventory changed; run UPDATE_ROUTE_GOLDEN=1 go test ./test/contract/http/app -run TestCollabRouteInventory")
	}
}
