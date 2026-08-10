package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestUnifiedAgentVersionsAndPerUserInstances(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Unified Owner", "unified-owner@example.com", "correct horse battery staple")
	member, _ := database.CreateUser("Unified Member", "unified-member@example.com", "correct horse battery staple")
	other, _ := database.CreateUser("Unified Other", "unified-other@example.com", "correct horse battery staple")
	space := createTestSpace(t, database, ctx, owner.ID, "Unified agents")
	for _, user := range []*User{member, other} {
		invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, user.Email)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.RespondToSpaceInvite(ctx, user.ID, invite.ID, true); err != nil {
			t.Fatal(err)
		}
	}

	workflowDraft, err := database.SaveSpaceStudioResource(ctx, owner.ID, SpaceStudioResource{SpaceID: space.ID, Kind: "workflow", Name: "Private summary", Description: "Summarize safely", Definition: unifiedTestDefinition("agent_task", "agent.reason", "read"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if workflowDraft.ActiveWorkflow != nil {
		t.Fatal("saving a draft created an immutable version")
	}
	metadata := unifiedTestMetadata("summarize", false)
	version, err := database.CreateWorkflowVersion(ctx, owner.ID, space.ID, workflowDraft.ID, "2.0.1", metadata, workflowDraft.Definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveSpaceStudioResource(ctx, member.ID, *workflowDraft); !errors.Is(err, ErrLibraryForbidden) {
		t.Fatalf("member workflow edit err=%v", err)
	}
	if _, err := database.CreateSpaceRun(ctx, member.ID, space.ID, "workflow", workflowDraft.ID, "test", "summarize", json.RawMessage(`{"prompt":"no"}`)); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("standalone workflow run err=%v", err)
	}

	agent, err := database.SaveSpaceStudioResource(ctx, owner.ID, SpaceStudioResource{SpaceID: space.ID, Kind: "agent", Name: "Research Agent", Instructions: "Summarize with citations.", Enabled: true, Status: "available", RuntimeKind: "cloud", AccessPolicy: json.RawMessage(`{"mode":"space","allowedUserIds":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if agent.ActiveWorkflow != nil || agent.ActiveWorkflowVersionID != "" {
		t.Fatal("Agent unexpectedly requires an embedded workflow")
	}
	published, err := database.PublishAgentVersion(ctx, owner.ID, space.ID, agent.ID, []AgentVersionWorkflow{{WorkflowVersionID: version.ID, Alias: "summary", Enabled: true}})
	if err != nil || len(published.Workflows) != 1 {
		t.Fatalf("published=%#v err=%v", published, err)
	}
	if _, err := database.PublishAgentVersion(ctx, member.ID, space.ID, agent.ID, nil); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("non-creator publish err=%v", err)
	}

	memberInstance, err := database.EnsureAgentInstance(ctx, member.ID, space.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	otherInstance, err := database.EnsureAgentInstance(ctx, other.ID, space.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if memberInstance.ID == otherInstance.ID || memberInstance.UserID == otherInstance.UserID {
		t.Fatalf("instances leaked: %#v %#v", memberInstance, otherInstance)
	}
	if allowed, err := database.AgentInstanceCapabilityAllowed(ctx, member.ID, memberInstance.ID, "tasks.update", "write"); err != nil || !allowed {
		t.Fatalf("default Task grant = %v, %v", allowed, err)
	}
	memberInstance, err = database.UpdateAgentInstanceCapabilityGrants(ctx, member.ID, memberInstance.ID, json.RawMessage(`[{"capability":"tasks.query","risk":"read"}]`))
	if err != nil || !AgentCapabilityGranted(memberInstance.CapabilityGrants, "tasks.query", "read") {
		t.Fatalf("updated grants = %#v, %v", memberInstance, err)
	}
	if allowed, err := database.AgentInstanceCapabilityAllowed(ctx, member.ID, memberInstance.ID, "tasks.update", "write"); err != nil || allowed {
		t.Fatalf("revoked Task grant = %v, %v", allowed, err)
	}
	if _, err := database.UpdateAgentInstanceCapabilityGrants(ctx, member.ID, otherInstance.ID, json.RawMessage(`[]`)); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("cross-user capability update err=%v", err)
	}
	if _, err := database.ConfigureInstanceWorkflow(ctx, member.ID, otherInstance.ID, version.ID, true, json.RawMessage(`{}`), json.RawMessage(`{"granted":true}`)); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("cross-user config err=%v", err)
	}
	if _, err := database.ConfigureInstanceWorkflow(ctx, member.ID, memberInstance.ID, version.ID, true, json.RawMessage(`{"cron":"0 9 * * *","timezone":"America/Los_Angeles"}`), json.RawMessage(`{"granted":true}`)); err != nil {
		t.Fatal(err)
	}
	connection, err := database.SaveSpaceIntegration(ctx, member.ID, SpaceIntegration{SpaceID: space.ID, Provider: "slack", DisplayName: "Member Slack", CredentialReference: "vault/member/slack", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	memberInstance, err = database.UpdateAgentInstanceConnections(ctx, member.ID, memberInstance.ID, map[string]string{"slack": connection.ID})
	if err != nil || memberInstance.ConnectionBindings["slack"] != connection.ID {
		t.Fatalf("member bindings=%#v err=%v", memberInstance, err)
	}
	if _, err := database.UpdateAgentInstanceConnections(ctx, other.ID, otherInstance.ID, map[string]string{"slack": connection.ID}); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("cross-user connection binding err=%v", err)
	}
	if connections, err := database.SpaceIntegrations(ctx, other.ID, space.ID); err != nil || len(connections) != 1 || connections[0].CredentialReference != "" || connections[0].ConnectedByUserID != member.ID {
		t.Fatalf("sanitized Space connections=%#v err=%v", connections, err)
	}

	chatRun, err := database.CreateAgentRun(ctx, AgentRunRequest{RequestingMemberID: member.ID, SpaceID: space.ID, AgentID: agent.ID, SourceType: "direct", CapabilityID: "chat", Input: json.RawMessage(`{"prompt":"hello"}`), TriggerKind: "manual"})
	if err != nil || chatRun.WorkflowVersionID != "" || chatRun.AgentInstanceID != memberInstance.ID || chatRun.AgentVersionID != published.ID {
		t.Fatalf("chat run=%#v err=%v", chatRun, err)
	}
	workflowRun, err := database.CreateAgentRun(ctx, AgentRunRequest{RequestingMemberID: member.ID, SpaceID: space.ID, AgentID: agent.ID, SourceType: "direct", CapabilityID: "summarize", Input: json.RawMessage(`{"prompt":"summarize"}`), TriggerKind: "manual"})
	if err != nil || workflowRun.WorkflowVersionID != version.ID || workflowRun.AgentInstanceID != memberInstance.ID {
		t.Fatalf("workflow run=%#v err=%v", workflowRun, err)
	}
	scheduledRun, err := database.CreateAgentRun(ctx, AgentRunRequest{RequestingMemberID: member.ID, SpaceID: space.ID, AgentID: agent.ID, SourceType: "schedule", CapabilityID: "summarize", Input: json.RawMessage(`{"prompt":"daily"}`), TriggerKind: "schedule"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.FinishSpaceRun(ctx, scheduledRun.ID, "completed", json.RawMessage(`{"text":"private digest"}`), ""); err != nil {
		t.Fatal(err)
	}
	if inbox, err := database.SpaceInbox(ctx, member.ID, "mentions", 100); err != nil || !containsWorkflowInbox(inbox, scheduledRun.ID) {
		t.Fatalf("private proactive inbox=%#v err=%v", inbox, err)
	}

	agent.Instructions = "Updated instructions"
	agent, err = database.SaveSpaceStudioResource(ctx, owner.ID, *agent)
	if err != nil {
		t.Fatal(err)
	}
	newPublished, err := database.PublishAgentVersion(ctx, owner.ID, space.ID, agent.ID, []AgentVersionWorkflow{{WorkflowVersionID: version.ID, Alias: "summary", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	memberInstance, err = database.EnsureAgentInstance(ctx, member.ID, space.ID, agent.ID)
	if err != nil || !memberInstance.UpdateAvailable || memberInstance.AgentVersionID != published.ID {
		t.Fatalf("pinned instance=%#v err=%v", memberInstance, err)
	}
	if _, err := database.UpdateAgentInstance(ctx, member.ID, memberInstance.ID); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("active instance update err=%v", err)
	}
	if _, err := database.FinishSpaceRun(ctx, chatRun.ID, "completed", json.RawMessage(`{"text":"done"}`), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := database.FinishSpaceRun(ctx, workflowRun.ID, "completed", json.RawMessage(`{"text":"done"}`), ""); err != nil {
		t.Fatal(err)
	}
	memberInstance, err = database.UpdateAgentInstance(ctx, member.ID, memberInstance.ID)
	if err != nil || memberInstance.AgentVersionID != newPublished.ID || string(memberInstance.CapabilityGrants) != "[]" {
		t.Fatalf("updated instance=%#v err=%v", memberInstance, err)
	}
}

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
