package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func TestPrivateSpaceConversationCreatesTaskThroughServerOwnedTool(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Space Chat Owner", "space-chat-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Agentic Space")
	if err != nil {
		t.Fatal(err)
	}

	result, err := api.TestingExecuteSpaceConversationTaskTool(
		ctx,
		database,
		owner.ID,
		space.ID,
		"",
		"Create a task called Review beta readiness",
		"tasks.create",
		json.RawMessage(`{"title":"Review beta readiness","priority":"high"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var created SpaceTask
	if err := json.Unmarshal(result, &created); err != nil {
		t.Fatal(err)
	}
	if created.Title != "Review beta readiness" || created.Priority != "high" || created.CreatedByUserID != owner.ID {
		t.Fatalf("created Task = %#v", created)
	}
	page, err := database.SpaceTaskPage(ctx, owner.ID, space.ID, SpaceTaskQuery{Search: "beta readiness", Limit: 10})
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].ID != created.ID {
		t.Fatalf("persisted Tasks = %#v, %v", page.Tasks, err)
	}
}

func TestPrivateSpaceConversationTaskWriteRechecksMemberPermission(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Space Tool Owner", "space-tool-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Space Tool Viewer", "space-tool-viewer@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Permission Space")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := database.SetSpaceMemberPermission(ctx, owner.ID, space.ID, member.ID, PermissionTasksManage, "deny"); err != nil {
		t.Fatal(err)
	}

	_, err = api.TestingExecuteSpaceConversationTaskTool(
		ctx,
		database,
		member.ID,
		space.ID,
		"",
		"Create a task called Forbidden task",
		"tasks.create",
		json.RawMessage(`{"title":"Forbidden task"}`),
	)
	if !errors.Is(err, workflowv2.ErrCapabilityDenied) {
		t.Fatalf("task write error = %v, want capability denied", err)
	}
}

func TestPrivateSpaceAgentSendsExactMessageThroughServerOwnedToolbox(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Space Message Owner", "space-message-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Message Space")
	if err != nil {
		t.Fatal(err)
	}
	personal, err := database.CreatePersonalAgent(ctx, owner.ID, PersonalAgent{
		Name: "Messenger", ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite",
		ToolPermissions: json.RawMessage(`{"read":true,"write":true,"grants":[{"capability":"messages.send","risk":"write"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddSpaceAgentMembership(
		ctx, owner.ID, space.ID, SpaceAgentMembershipInput{AgentID: personal.ID},
	); err != nil {
		t.Fatal(err)
	}

	result, err := api.TestingExecuteSpaceConversationTool(
		ctx, database, owner.ID, space.ID, personal.ID,
		"Tell everyone Stone is off for today", "messages.send",
		json.RawMessage(`{"message":"Stone is off for today"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var sent struct {
		MessageID string `json:"message_id"`
	}
	if json.Unmarshal(result, &sent) != nil || sent.MessageID == "" {
		t.Fatalf("send result = %s", result)
	}
	messages, err := database.SpaceMessages(ctx, owner.ID, space.ID, 0, 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("shared Agent messages = %#v, %v", messages, err)
	}
	rawContent, _ := json.Marshal(messages[0].Content)
	if messages[0].SenderAgentID != personal.ID ||
		!strings.Contains(string(rawContent), "Stone is off for today") {
		t.Fatalf("shared Agent messages = %#v, %v", messages, err)
	}
}
