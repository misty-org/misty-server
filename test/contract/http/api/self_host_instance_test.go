package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type instanceStoreStub struct {
	state db.InstanceState
	err   error
}

func (stub instanceStoreStub) SelfHostedInstanceState(context.Context, string) (db.InstanceState, error) {
	return stub.state, stub.err
}

func TestInstanceDescriptorAdvertisesSelfHostedIsolation(t *testing.T) {
	t.Setenv("MISTY_DEPLOYMENT_MODE", "self_hosted")
	t.Setenv("MISTY_INSTANCE_NAME", "Studio LAN")
	t.Setenv("MISTY_LIBRARY_BACKEND", "filesystem")
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/instance", nil)
	api.Instance(instanceStoreStub{state: db.InstanceState{
		ServerID:          "server_00000000-0000-0000-0000-000000000001",
		DisplayName:       "Studio LAN",
		BootstrapRequired: true,
	}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var descriptor api.InstanceDescriptor
	if err := json.NewDecoder(recorder.Body).Decode(&descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.Deployment != "self_hosted" || !descriptor.BootstrapRequired || descriptor.Registration != "invitation" {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	if descriptor.Capabilities.HostedBilling || descriptor.Capabilities.HostedIntegrations || descriptor.Capabilities.HostedAI {
		t.Fatalf("self-hosted descriptor advertised Hosted-only capabilities: %#v", descriptor.Capabilities)
	}
	if descriptor.Capabilities.StorageBackend != "filesystem" {
		t.Fatalf("storage backend = %q", descriptor.Capabilities.StorageBackend)
	}
}

func TestInstanceConfigDefaultsToHosted(t *testing.T) {
	t.Setenv("MISTY_DEPLOYMENT_MODE", "")
	t.Setenv("MISTY_INSTANCE_NAME", "")
	t.Setenv("MISTY_LIBRARY_BACKEND", "")
	config := api.InstanceConfigFromEnv()
	if config.Deployment != "hosted" || config.Name != "Misty Hosted" {
		t.Fatalf("config = %#v", config)
	}
	if !config.Capabilities.HostedBilling || !config.Capabilities.HostedIntegrations || !config.Capabilities.HostedAI {
		t.Fatalf("hosted capabilities = %#v", config.Capabilities)
	}
}

func TestSelfHostedFeatureGateBlocksHostedOnlyRoutes(t *testing.T) {
	t.Setenv("MISTY_DEPLOYMENT_MODE", "self_hosted")
	called := false
	handler := api.SelfHostedFeatureGate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	for _, path := range []string{"/api/billing/portal-session", "/api/ai/complete", "/api/spaces/space_1/integrations/notion/connection"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		if recorder.Code != http.StatusNotImplemented {
			t.Fatalf("%s status = %d", path, recorder.Code)
		}
	}
	if called {
		t.Fatal("blocked route reached its handler")
	}
}
