package unit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

// Every tool any run can be offered declares its layer, and the startup check
// passes on the catalog the server ships.
func TestEveryAgentToolDeclaresALayer(t *testing.T) {
	if err := api.TestingValidateAgentToolLayers(); err != nil {
		t.Fatal(err)
	}
	catalog := api.TestingAgentToolCatalog()
	if len(catalog) < 40 {
		t.Fatalf("catalog has only %d tools; the startup check would miss most of them", len(catalog))
	}
	for _, descriptor := range catalog {
		if descriptor.Layer != agenttools.LayerPublic && descriptor.Layer != agenttools.LayerPrivate {
			t.Errorf("%s declares no layer", descriptor.Name)
		}
	}
}

func TestRegisteringAToolWithoutALayerFails(t *testing.T) {
	handler := func(context.Context, agenttools.Invocation, serveragent.ToolRequest) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}
	for _, layer := range []agenttools.Layer{"", "shared"} {
		_, err := agenttools.New(agenttools.Registration{
			Descriptor: agenttools.Descriptor{Name: "probe.read", Description: "Probe", Risk: serveragent.RiskRead, Layer: layer},
			Handler:    handler,
		})
		if !errors.Is(err, agenttools.ErrInvalidRegistration) {
			t.Fatalf("layer %q: error = %v, want ErrInvalidRegistration", layer, err)
		}
	}
}

// Tools that reach a person's own trees are private: memory, devices,
// connected apps, agent settings and the account's Spaces.
func TestPrivateTreesArePrivateTools(t *testing.T) {
	layers := map[string]agenttools.Layer{}
	for _, descriptor := range api.TestingAgentToolCatalog() {
		layers[descriptor.Name] = descriptor.Layer
	}
	for _, name := range []string{"memory.pref.list", "memory.pref.update", "agent.configure", "agent.list", "browser.inspect"} {
		if layer, ok := layers[name]; ok && layer != agenttools.LayerPrivate {
			t.Errorf("%s is %s, want private", name, layer)
		}
	}
	for name, layer := range layers {
		if (len(name) > 7 && name[:7] == "memory.") && layer != agenttools.LayerPrivate {
			t.Errorf("%s is %s, want private", name, layer)
		}
	}
}
