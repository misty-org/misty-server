package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kannachi323/misty/server/internal/platform/db"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
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
	t.Setenv("MISTY_AGENT_MODEL", "anthropic/claude-sonnet-5-5")
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
	if !descriptor.Capabilities.HostedIntegrations || !descriptor.Capabilities.HostedAI || !descriptor.Models.Operator {
		t.Fatalf("hosted capabilities = %#v models = %#v", descriptor.Capabilities, descriptor.Models)
	}
}

func TestInstanceDescriptorSaysWhenAccountsBringTheirOwnModels(t *testing.T) {
	t.Setenv("MISTY_AGENT_MODEL", "")
	t.Setenv("MISTY_ENVIRONMENT", "production")
	recorder := httptest.NewRecorder()
	api.Instance(instanceStoreStub{state: db.InstanceState{ServerID: "server_1", DisplayName: "Misty"}}, "filesystem").ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/instance", nil))
	var raw map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	models, _ := raw["models"].(map[string]any)
	if models["operator"] != false || raw["capabilities"].(map[string]any)["storage_backend"] != "filesystem" {
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
