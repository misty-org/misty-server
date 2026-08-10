package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestDirectWorkflowRunCreatesApproval(t *testing.T) {
	t.Skip("standalone workflow execution was intentionally removed in workflow v2")
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Workflow Approval Owner", "workflow-approval-owner@example.com", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	spaces, err := database.ListSpaces(ctx, owner.ID)
	if err != nil || len(spaces) != 1 {
		t.Fatalf("owner Spaces = %#v, %v", spaces, err)
	}
	metadata := architectureMetadata()
	metadata.Capabilities[0].ReadOnly = false
	metadata.Capabilities[0].Destructive = true
	metadata.Capabilities[0].ConfirmationRequired = true
	workflow, err := database.SaveSpaceStudioResource(ctx, owner.ID, SpaceStudioResource{SpaceID: spaces[0].ID, Kind: "workflow", Name: "Approval Workflow", Definition: mustTestRaw(map[string]any{"metadata": metadata, "nodes": []any{}, "edges": []any{}}), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := database.CreateSpaceRun(ctx, owner.ID, spaces[0].ID, "workflow", workflow.ID, "test", metadata.Capabilities[0].ID, json.RawMessage(`{"prompt":"approve this"}`))
	if err != nil || run.State != "awaiting_approval" {
		t.Fatalf("direct workflow run = %#v, %v", run, err)
	}
	approvals, err := database.RunApprovals(ctx, owner.ID, run.ID)
	if err != nil || len(approvals) != 1 || approvals[0].State != "pending" {
		t.Fatalf("direct workflow approvals = %#v, %v", approvals, err)
	}
	actions, err := database.RunActions(ctx, owner.ID, run.ID)
	if err != nil || len(actions) != 1 || actions[0].State != "proposed" || !actions[0].Destructive {
		t.Fatalf("direct workflow actions = %#v, %v", actions, err)
	}
	if _, err := database.CancelSpaceRun(ctx, owner.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := database.RetrySpaceRun(ctx, owner.ID, run.ID)
	if err != nil || retry.State != "awaiting_approval" {
		t.Fatalf("destructive workflow retry = %#v, %v", retry, err)
	}
	retryApprovals, err := database.RunApprovals(ctx, owner.ID, retry.ID)
	if err != nil || len(retryApprovals) != 1 || retryApprovals[0].State != "pending" {
		t.Fatalf("destructive workflow retry approvals = %#v, %v", retryApprovals, err)
	}
	workflow.Enabled = false
	workflow, err = database.SaveSpaceStudioResource(ctx, owner.ID, *workflow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.DecideRunApproval(ctx, owner.ID, retry.ID, true); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("disabled workflow approval error = %v", err)
	}
	if _, err := database.CancelSpaceRun(ctx, owner.ID, retry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RetrySpaceRun(ctx, owner.ID, retry.ID); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("disabled workflow retry error = %v", err)
	}
	if err := database.DeleteSpaceStudioResource(ctx, owner.ID, spaces[0].ID, "workflow", workflow.ID); err != nil {
		t.Fatalf("delete workflow with run history: %v", err)
	}
	retained, err := database.SpaceRun(ctx, owner.ID, retry.ID)
	if err != nil || retained.State != "canceled" || retained.WorkflowVersionID != "" || retained.WorkflowVersion == "" {
		t.Fatalf("retained run after workflow deletion = %#v, %v", retained, err)
	}
	retryApprovals, err = database.RunApprovals(ctx, owner.ID, retry.ID)
	if err != nil || len(retryApprovals) != 1 || retryApprovals[0].State != "canceled" {
		t.Fatalf("approval after workflow deletion = %#v, %v", retryApprovals, err)
	}
}

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
