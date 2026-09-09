package db

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func containsWorkflowInbox(items []SpaceInboxItem, runID string) bool {
	for _, item := range items {
		if item.Kind == "workflow" && strings.Contains(string(item.Payload), runID) {
			return true
		}
	}
	return false
}

func unifiedTestDefinition(kind, capability, risk string) json.RawMessage {
	return mustTestRaw(map[string]any{"formatVersion": 2, "inputs": map[string]any{"type": "object"}, "outputs": map[string]any{"type": "object"}, "capabilities": []map[string]any{{"capability": capability, "risk": risk}}, "nodes": []map[string]any{{"id": "task", "kind": kind, "kindVersion": 1, "label": "Task", "config": map[string]any{}, "outputSchema": map[string]any{"type": "object"}, "retry": map[string]any{"maxAttempts": 3, "cooldownSeconds": 60}, "errors": map[string]any{"mode": "fail"}}}, "edges": []any{}, "dependencies": []any{}})
}

func unifiedTestMetadata(id string, destructive bool) WorkflowMetadata {
	return WorkflowMetadata{Capabilities: []WorkflowCapability{{ID: id, Name: id, Description: id, Inputs: []WorkflowField{{Name: "prompt", Type: "string"}}, Outputs: []WorkflowField{{Name: "result", Type: "object"}}, ReadOnly: !destructive, Destructive: destructive, ConfirmationRequired: destructive}}, RequiredIntegrations: []string{}, RequiredPermissions: []string{}, Runtime: WorkflowRuntime{Kind: "misty-cloud", Compatibility: "workflow-v2"}, Tags: []string{"workflow-v2"}}
}

func TestWorkflowChecksumDetectsMutation(t *testing.T) {
	metadata := unifiedTestMetadata("summary", false)
	definition := unifiedTestDefinition("agent_task", "agent.reason", "read")
	metadataRaw, _ := json.Marshal(metadata)
	var value any
	_ = json.Unmarshal(definition, &value)
	canonical, _ := json.Marshal(value)
	digest := sha256.Sum256(append(append([]byte{}, metadataRaw...), canonical...))
	version := &WorkflowVersion{Metadata: metadata, Definition: canonical, ChecksumSHA256: hex.EncodeToString(digest[:])}
	if !TestingWorkflowChecksumValid(version) {
		t.Fatal("valid checksum rejected")
	}
	version.Definition = unifiedTestDefinition("read_content", "content.read", "read")
	if TestingWorkflowChecksumValid(version) {
		t.Fatal("mutated definition retained checksum validity")
	}
}
