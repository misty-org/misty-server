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

func TestInstanceDescriptorAdvertisesSelfHostedProviders(t *testing.T) {
	t.Setenv("MISTY_BILLING_ADAPTER", "none")
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
	if descriptor.Capabilities.HostedBilling || !descriptor.Capabilities.HostedIntegrations || !descriptor.Capabilities.HostedAI {
		t.Fatalf("self-hosted descriptor advertised unexpected capabilities: %#v", descriptor.Capabilities)
	}
	if descriptor.Capabilities.StorageBackend != "filesystem" {
		t.Fatalf("storage backend = %q", descriptor.Capabilities.StorageBackend)
	}
}

func TestInstanceConfigDefaultsToIndependentSelfHosted(t *testing.T) {
	t.Setenv("MISTY_BILLING_ADAPTER", "none")
	t.Setenv("MISTY_DEPLOYMENT_MODE", "")
	t.Setenv("MISTY_INSTANCE_NAME", "")
	t.Setenv("MISTY_LIBRARY_BACKEND", "")
	config := api.InstanceConfigFromEnv()
	if config.Deployment != "self_hosted" || config.Name != "Misty Self-hosted" {
		t.Fatalf("config = %#v", config)
	}
	if config.Capabilities.HostedBilling || !config.Capabilities.HostedIntegrations || !config.Capabilities.HostedAI {
		t.Fatalf("hosted capabilities = %#v", config.Capabilities)
	}
}
