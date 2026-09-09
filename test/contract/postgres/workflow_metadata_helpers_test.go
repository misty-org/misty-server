package db

import (
	"encoding/json"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func architectureMetadata() WorkflowMetadata {
	return WorkflowMetadata{
		Capabilities: []WorkflowCapability{
			{ID: "summarize-files", Name: "Summarize files", Description: "Summarize documents and files", Inputs: []WorkflowField{{Name: "prompt", Type: "string", Required: true}}, Outputs: []WorkflowField{{Name: "summary", Type: "string"}}, ReadOnly: true, Tags: []string{"summarize", "documents"}},
			{ID: "organize-folders", Name: "Organize folders", Description: "Organize campaign folders and files", Inputs: []WorkflowField{{Name: "prompt", Type: "string", Required: true}}, Outputs: []WorkflowField{{Name: "actions", Type: "array"}}, Destructive: true, ConfirmationRequired: true, Tags: []string{"organize", "folders"}},
		},
		RequiredIntegrations: []string{}, RequiredPermissions: []string{}, Runtime: WorkflowRuntime{Kind: "misty-cloud", Compatibility: "1"}, Tags: []string{"operations"},
	}
}

func mustTestRaw(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }

func containsSpaceRun(items []SpaceRun, runID string) bool {
	for _, item := range items {
		if item.ID == runID {
			return true
		}
	}
	return false
}
