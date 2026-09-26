package db

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"

	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

func sdkInstallFixture(t *testing.T, appID string) (providerConfiguration, ed25519.PrivateKey) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return providerConfiguration{AppID: appID, Version: "1.0.0", PermissionVersion: 1, Scopes: []string{"capabilities.providers.write", "capabilities.read", "habits.list"}, Capabilities: providerDefinitions{Protocol: 1, Providers: []cap.Provider{{ID: appID + "/backend", Version: 1, Label: "Habits", Route: cap.Route{Kind: "backend", ConnectionID: "10000000-0000-4000-8000-000000000001"}, Capabilities: []cap.Definition{{Name: "habits.list", Version: 1, Description: "List recorded habits", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"string"}}`), RequiredScopes: []string{"habits.list"}, Effects: cap.Effects{Kind: "read", Incidental: []string{}, Approval: "none", Retry: "read_only"}}}}}}}, key
}

type providerConfiguration struct {
	AppID, Version    string
	PermissionVersion int
	Scopes            []string
	Capabilities      providerDefinitions
}
type providerDefinitions struct {
	Protocol  int
	Providers []cap.Provider
}
