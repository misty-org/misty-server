package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/misty-org/misty-server/internal/platform/db"
	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

type instanceStoreStub struct {
	state db.InstanceState
	err   error
}

func (stub instanceStoreStub) InstanceState(context.Context, string) (db.InstanceState, error) {
	return stub.state, stub.err
}

func TestInstanceDescriptorIsHosted(t *testing.T) {
	t.Setenv("MISTY_INSTANCE_NAME", "")
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/instance", nil)
	api.Instance(instanceStoreStub{state: db.InstanceState{
		ServerID:    "server_00000000-0000-0000-0000-000000000001",
		DisplayName: "Misty",
	}}, "s3").ServeHTTP(recorder, request)

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
	if !descriptor.Capabilities.HostedIntegrations {
		t.Fatalf("hosted capabilities = %#v", descriptor.Capabilities)
	}
}

func TestInstanceDescriptorNamesTheStorageBackend(t *testing.T) {
	recorder := httptest.NewRecorder()
	api.Instance(instanceStoreStub{state: db.InstanceState{ServerID: "server_1", DisplayName: "Misty"}}, "filesystem").ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/instance", nil))
	var raw map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if raw["capabilities"].(map[string]any)["storage_backend"] != "filesystem" || raw["models"] != nil {
		t.Fatalf("descriptor = %#v", raw)
	}
}

func TestInstanceDescriptorReportsUnavailableStore(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/instance", nil)
	api.Instance(instanceStoreStub{err: context.DeadlineExceeded}, "s3").ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}
