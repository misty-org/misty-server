package modelruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/byok"
)

func TestRouteForUsesTheAccountKeyThenTheOperatorsThenRefuses(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	t.Setenv("MISTY_AGENT_MODEL", "")
	byok.Default.Put("route-account", &byok.Credentials{Version: 1, Providers: map[string]byok.Credential{"nvidia": {Provider: "nvidia", APIKey: "nv-key"}}, Models: byok.Models{Agent: "nim/x/y"}})
	t.Cleanup(func() { byok.Default.Forget("route-account") })
	route, err := RouteFor(context.Background(), "route-account", "nim/moonshotai/kimi", "low")
	if err != nil || route.Provider != "account" || route.Credential == nil || route.Credential.Provider != "nvidia" || route.Credential.APIKey != "nv-key" {
		t.Fatalf("account route = %+v, %v", route, err)
	}
	raw, _ := json.Marshal(route)
	if !strings.Contains(string(raw), `"credential":{"provider":"nvidia","apiKey":"nv-key"}`) {
		t.Fatalf("route JSON = %s", raw)
	}
	if _, err := RouteFor(context.Background(), "route-account", "anthropic/claude", ""); !errors.Is(err, byok.ErrModelKeyNeeded) {
		t.Fatalf("another provider without a key = %v", err)
	}
	t.Setenv("MISTY_AGENT_MODEL", "anthropic/claude")
	if route, err := RouteFor(context.Background(), "route-account", "anthropic/claude", ""); err != nil || route.Provider != "instance" || route.Credential != nil {
		t.Fatalf("operator route = %+v, %v", route, err)
	}
	if For(&aimodels.Resolved{Model: "nim/x/y", Credential: &byok.Credential{Provider: "nvidia", APIKey: "k"}}).Provider != "account" || For(nil).Provider != "instance" {
		t.Fatal("For")
	}
}
