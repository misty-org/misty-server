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

func (stub instanceStoreStub) InstanceState(context.Context, string) (db.InstanceState, error) {
	return stub.state, stub.err
}

func TestInstanceDescriptorIsHosted(t *testing.T) {
	t.Setenv("MISTY_BILLING_ADAPTER", "http")
	t.Setenv("MISTY_INSTANCE_NAME", "")
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/instance", nil)
	api.Instance(instanceStoreStub{state: db.InstanceState{
		ServerID:    "server_00000000-0000-0000-0000-000000000001",
		DisplayName: "Misty",
	}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var descriptor api.InstanceDescriptor
	if err := json.NewDecoder(recorder.Body).Decode(&descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.Deployment != "hosted" || descriptor.BootstrapRequired || descriptor.Registration != "open" || descriptor.Name != "Misty" {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	if !descriptor.Capabilities.HostedBilling || !descriptor.Capabilities.HostedIntegrations || !descriptor.Capabilities.HostedAI {
		t.Fatalf("hosted capabilities = %#v", descriptor.Capabilities)
	}
}

func TestInstanceDescriptorReportsUnavailableStore(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/instance", nil)
	api.Instance(instanceStoreStub{err: context.DeadlineExceeded}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}
