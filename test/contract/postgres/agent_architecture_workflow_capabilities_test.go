package db

import (
	"encoding/json"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestWorkflowMetadataSupportsMultipleCapabilitiesAndProtectsDestructiveActions(t *testing.T) {
	metadata := architectureMetadata()
	if err := ValidateWorkflowMetadata(metadata); err != nil {
		t.Fatalf("valid multi-capability metadata rejected: %v", err)
	}
	metadata.Capabilities[1].ConfirmationRequired = false
	if err := ValidateWorkflowMetadata(metadata); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("destructive capability without confirmation error = %v", err)
	}
	metadata = architectureMetadata()
	metadata.RequiredPermissions = []string{"made.up.permission"}
	if err := ValidateWorkflowMetadata(metadata); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("unknown permission declaration error = %v", err)
	}
}

func TestStructuredCapabilityRoutingScore(t *testing.T) {
	metadata := architectureMetadata()
	words := TestingRoutingWords("Please organize the campaign folders")
	organize := TestingRoutingScore(words, metadata.Capabilities[1])
	summarize := TestingRoutingScore(words, metadata.Capabilities[0])
	if organize <= summarize {
		t.Fatalf("structured router scores organize=%d summarize=%d", organize, summarize)
	}
}

func TestWorkflowCapabilityInputAndRuntimeBoundaryValidation(t *testing.T) {
	capability := WorkflowCapability{Inputs: []WorkflowField{
		{Name: "prompt", Type: "string", Required: true},
		{Name: "limit", Type: "integer"},
		{Name: "options", Type: "object"},
	}}
	if err := TestingValidateCapabilityInput(capability, json.RawMessage(`{"prompt":"organize","limit":3,"options":{"dryRun":true},"context":"allowed"}`)); err != nil {
		t.Fatalf("valid structured input rejected: %v", err)
	}
	for _, raw := range []string{
		`{"limit":3}`,
		`{"prompt":"   "}`,
		`{"prompt":false}`,
		`{"prompt":"organize","limit":2.5}`,
		`{"prompt":"organize","options":[]}`,
	} {
		if err := TestingValidateCapabilityInput(capability, json.RawMessage(raw)); !errors.Is(err, ErrSpaceInvalid) {
			t.Fatalf("invalid capability input %s error = %v", raw, err)
		}
	}

	localDefinition := unifiedTestDefinition("read_file", "files.read", "read")
	cloud := architectureMetadata()
	if err := TestingValidateWorkflowVersionDefinition(cloud, localDefinition); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("workflow accepted an unregistered v1 node: %v", err)
	}
	validV2 := unifiedTestDefinition("changed_files", "files.read", "read")
	if err := TestingValidateWorkflowVersionDefinition(cloud, validV2); err != nil {
		t.Fatalf("registered v2 device-leased node rejected: %v", err)
	}
	if permission, ok := TestingWorkflowPermissionSpacePermission("files.read"); !ok || permission != PermissionLibraryView {
		t.Fatalf("files.read permission mapping = %q, %v", permission, ok)
	}
	if permission, ok := TestingWorkflowPermissionSpacePermission("files.write"); !ok || permission != PermissionLibraryEdit {
		t.Fatalf("files.write permission mapping = %q, %v", permission, ok)
	}
}
