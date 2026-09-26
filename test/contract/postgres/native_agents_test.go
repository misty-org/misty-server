package db

import (
	"context"
	"encoding/json"
	"errors"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"testing"
	"time"
)

func TestNativeAgentsPrivacyMemoryAndLeases(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Agent owner", "native-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other owner", "native-other@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Launch")
	secondSpace := createTestSpace(t, database, ctx, owner.ID, "Private")
	misty, err := database.EnsureAskIdentity(ctx, owner.ID, "google/gemini-2.5-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	profile := AgentProfileInput{Name: "Communications", Role: "Manage launch communications", Instructions: "Use clear language", ModelMode: "automatic", Enabled: true}
	// Legacy model fields cannot override managed policy.
	profile.ModelMode = "pinned"
	profile.ModelID = "anthropic/legacy-model"
	profile.ReasoningEffort = "low"
	agent, err := database.SavePersonalAgent(ctx, owner.ID, "", profile)
	if err != nil {
		t.Fatal(err)
	}
	if agent.ModelMode != "automatic" || agent.ModelID != "openai/gpt-6-astra" || agent.ReasoningEffort != "high" {
		t.Fatalf("unmanaged model: %+v", agent)
	}
	if agent.SystemManaged || agent.ID == misty.ID {
		t.Fatalf("invalid personal identity: %+v", agent)
	}
	agents, err := database.PersonalAgents(ctx, other.ID)
	if err != nil || len(agents) != 0 {
		t.Fatalf("private agents leaked: %+v %v", agents, err)
	}
	conversation, err := database.CreatePersonalAgentConversation(ctx, owner.ID, space.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = database.BindConversationAgent(ctx, owner.ID, conversation, misty.ID); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("conversation retarget: %v", err)
	}
	if err = database.BindConversationAgent(ctx, other.ID, conversation, agent.ID); err == nil {
		t.Fatal("conversation owner changed")
	}
	profile.Version = agent.Version
	profile.Role = "Coordinate launch work"
	updated, err := database.SavePersonalAgent(ctx, owner.ID, agent.ID, profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.SavePersonalAgent(ctx, owner.ID, agent.ID, profile); !errors.Is(err, ErrPersonalAgentConflict) {
		t.Fatalf("stale version overwrite: %v", err)
	}
	memory, err := database.RememberMistyMemory(ctx, owner.ID, RememberMistyMemoryInput{AgentID: agent.ID, SpaceID: space.ID, Kind: "preference", Content: "Use a warm brand voice"})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct{ user, agent, space string }{{other.ID, agent.ID, space.ID}, {owner.ID, misty.ID, space.ID}} {
		values, err := database.MistyMemoryContext(ctx, scope.user, scope.space, 20, scope.agent)
		if err == nil && len(values) > 0 {
			t.Fatalf("memory escaped owner/agent/Space: %+v", scope)
		}
	}
	if values, err := database.MistyMemoryContext(ctx, owner.ID, secondSpace.ID, 20, agent.ID); err != nil || len(values) != 1 || values[0].ID != memory.ID {
		t.Fatalf("account memory restricted to a Space: %v %v", values, err)
	}
	if err = database.UpdateAgentMemory(ctx, owner.ID, agent.ID, space.ID, memory.ID, "Use a concise brand voice"); err != nil {
		t.Fatal(err)
	}
	if err = database.ForgetMistyMemory(ctx, owner.ID, memory.ID, misty.ID); err == nil {
		t.Fatal("another agent forgot memory")
	}
	if err = database.ForgetMistyMemory(ctx, owner.ID, memory.ID, agent.ID); err != nil {
		t.Fatal(err)
	}
	lease := AgentExecutionLease{AgentID: agent.ID, SpaceID: space.ID, TaskID: "task-native-one", WindowLabel: "misty-agent-one"}
	if err = database.AcquireAgentExecution(ctx, owner.ID, lease); err != nil {
		t.Fatal(err)
	}
	competing := lease
	competing.TaskID = "task-native-two"
	competing.WindowLabel = "main"
	if err = database.AcquireAgentExecution(ctx, owner.ID, competing); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("concurrent agent execution: %v", err)
	}
	payload, _ := json.Marshal(map[string]string{"agent_id": agent.ID, "execution_mode": "team", "task_id": lease.TaskID, "window_label": lease.WindowLabel})
	record := &AIInvocationRecord{UserID: owner.ID, SpaceID: space.ID, RequestPayload: payload}
	if err = database.ValidateNativeAgentExecution(ctx, record); err != nil {
		t.Fatal(err)
	}

	persisted, _, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{ID: "invocation_native_recovery", UserID: owner.ID, SpaceID: space.ID, ConversationID: conversation, Mode: "quick", SurfaceID: "global", Trigger: "message", State: "running", IdempotencyKey: "native-recovery", RequestPayload: payload, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.JournalAgentToolboxAction(ctx, AgentToolboxAction{IdempotencyKey: "native-planner-write", UserID: owner.ID, SpaceID: space.ID, AgentID: agent.ID, RunID: persisted.ID, ToolName: "tasks.update", AuditEvent: "task.updated", Risk: "write", Source: "ai_invocation", Request: json.RawMessage(`{"title":"Launch Friday"}`)}, func() (json.RawMessage, error) {
		return json.RawMessage(`{"id":"planner-result","title":"Launch Friday"}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := database.NativeAgentConversationReceipts(ctx, owner.ID, agent.ID, space.ID, conversation, "next-invocation")
	if err != nil || len(receipts) != 1 || receipts[0].State != "completed" {
		t.Fatalf("recovery receipts: %+v %v", receipts, err)
	}
	for _, boundary := range []struct{ user, agent, space string }{{other.ID, agent.ID, space.ID}, {owner.ID, misty.ID, space.ID}} {
		receipts, err := database.NativeAgentConversationReceipts(ctx, boundary.user, boundary.agent, boundary.space, conversation, "next-invocation")
		if err != nil || len(receipts) != 0 {
			t.Fatalf("receipt boundary: %+v %v", receipts, err)
		}
	}
	if err = database.ReleaseAgentExecution(ctx, owner.ID, lease.TaskID); err != nil {
		t.Fatal(err)
	}
	if err = database.ValidateNativeAgentExecution(ctx, record); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("released lease still authorizes writes: %v", err)
	}
	activity, err := database.MistyActivity(ctx, owner.ID, space.ID, agent.ID)
	var entries []map[string]any
	if err != nil || json.Unmarshal(activity, &entries) != nil || len(entries) != 1 || entries[0]["state"] != "paused" {
		t.Fatalf("lost execution reported incorrectly: %s %v", activity, err)
	}
	lease.Renew = true
	if err = database.AcquireAgentExecution(ctx, owner.ID, lease); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("renew resurrects revoked task: %v", err)
	}
	if err = database.DeletePersonalAgent(ctx, owner.ID, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = database.AskExecutionContext(ctx, owner.ID, space.ID, updated.ID); err == nil {
		t.Fatal("deleted agent can execute")
	}
	if err = database.DeletePersonalAgent(ctx, owner.ID, misty.ID); err == nil {
		t.Fatal("default Misty deleted")
	}
}
