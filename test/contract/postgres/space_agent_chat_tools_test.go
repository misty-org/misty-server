package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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
	})
	if err != nil {
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

func TestMistyRoutesOnePersonPrivatelyAndPreservesStructuredMention(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Matthew Chen", "private-message-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Melissa Chen", "private-message-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Family")
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

	result, err := api.TestingExecuteSpaceConversationTool(
		ctx, database, owner.ID, space.ID, "",
		"Tell Melissa Chen to please finish the laundry by tonight", "messages.send",
		json.RawMessage(`{"message":"@Melissa Chen, please finish the laundry by tonight.","audience":"auto","recipientUserId":"`+member.ID+`"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var sent struct {
		MessageID      string `json:"message_id"`
		ConversationID string `json:"conversation_id"`
		Audience       string `json:"audience"`
	}
	if err := json.Unmarshal(result, &sent); err != nil || sent.MessageID == "" || sent.ConversationID == "" || sent.Audience != "private" {
		t.Fatalf("private send result = %s, %v", result, err)
	}
	shared, err := database.SpaceMessages(ctx, owner.ID, space.ID, 0, 10)
	if err != nil || len(shared) != 0 {
		t.Fatalf("private message leaked into shared chat: %#v, %v", shared, err)
	}
	messages, err := database.SpaceConversationMessages(ctx, member.ID, space.ID, sent.ConversationID, 0, 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("private conversation messages = %#v, %v", messages, err)
	}
	var origin struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(messages[0].Origin, &origin) != nil || messages[0].SenderKind != "system" || origin.Kind != "misty_assistant" {
		t.Fatalf("Misty identity provenance = %#v", messages[0])
	}
	if len(messages[0].Content) < 2 || messages[0].Content[0].Type != "mention" || messages[0].Content[0].UserID != member.ID || messages[0].Content[0].Label != member.Name {
		t.Fatalf("structured mention content = %#v", messages[0].Content)
	}

	groupResult, err := api.TestingExecuteSpaceConversationTool(
		ctx, database, owner.ID, space.ID, "",
		"Post in the group chat that Melissa Chen will finish the laundry tonight", "messages.send",
		json.RawMessage(`{"message":"@Melissa Chen will finish the laundry tonight.","audience":"auto","recipientUserId":"`+member.ID+`"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var groupSent struct {
		Audience string `json:"audience"`
	}
	if json.Unmarshal(groupResult, &groupSent) != nil || groupSent.Audience != "space" {
		t.Fatalf("group send result = %s", groupResult)
	}
	shared, err = database.SpaceMessages(ctx, member.ID, space.ID, 0, 10)
	if err != nil || len(shared) != 1 || shared[0].Content[0].Type != "mention" || shared[0].Content[0].UserID != member.ID {
		t.Fatalf("shared mention message = %#v, %v", shared, err)
	}

	_, err = api.TestingExecuteSpaceConversationTool(
		ctx, database, owner.ID, space.ID, "",
		"Send the laundry update", "messages.send",
		json.RawMessage(`{"message":"Laundry update","audience":"auto"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "Should I send this privately or in the Space chat?") {
		t.Fatalf("ambiguous audience error = %v", err)
	}
	shared, err = database.SpaceMessages(ctx, member.ID, space.ID, 0, 10)
	if err != nil || len(shared) != 1 {
		t.Fatalf("ambiguous message should not be sent: %#v, %v", shared, err)
	}
}

func TestSpaceAgentResolvesMemberAndCreatesAssignedTask(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Assignment Owner", "assignment-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Melissa Chen", "melissa-assignment@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Family")
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

	resolved, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Assign Melissa a chore", "members.resolve", json.RawMessage(`{"query":"Melissa"}`))
	if err != nil || !strings.Contains(string(resolved), member.ID) || !strings.Contains(string(resolved), `"resolved":true`) {
		t.Fatalf("resolved member = %s, %v", resolved, err)
	}
	createdRaw, err := api.TestingExecuteSpaceConversationTaskTool(ctx, database, owner.ID, space.ID, "", "Add a chore to the planner", "tasks.create", json.RawMessage(`{"title":"Wash the dishes","assigneeUserId":"`+member.ID+`","dueAt":"2026-08-19T19:00:00-07:00","dueTimezone":"America/Los_Angeles"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created SpaceTask
	if err := json.Unmarshal(createdRaw, &created); err != nil {
		t.Fatal(err)
	}
	if created.AssigneeUserID != member.ID || created.DueAt == nil || created.DueTimezone != "America/Los_Angeles" {
		t.Fatalf("assigned task = %#v", created)
	}
}

func TestSpaceAgentInterpretsTaskTimestampInTheSuppliedTimezone(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Date Owner", "date-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Date Space")
	if err != nil {
		t.Fatal(err)
	}
	createdRaw, err := api.TestingExecuteSpaceConversationTaskTool(ctx, database, owner.ID, space.ID, "", "Add a task", "tasks.create", json.RawMessage(`{"title":"Local date","dueAt":"2024-11-21T19:00:00","dueTimezone":"America/Los_Angeles"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created SpaceTask
	if json.Unmarshal(createdRaw, &created) != nil || created.DueAt == nil || created.DueAt.Format(time.RFC3339) != "2024-11-22T03:00:00Z" {
		t.Fatalf("local task timestamp = %s", createdRaw)
	}

	defaultedRaw, err := api.TestingExecuteSpaceConversationTaskTool(
		ctx, database, owner.ID, space.ID, "",
		`{"instruction":"Add an evening task","timezone":"America/Los_Angeles"}`,
		"tasks.create",
		json.RawMessage(`{"title":"Evening task","dueAt":"2024-11-21T19:00:00-08:00"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var defaulted SpaceTask
	if json.Unmarshal(defaultedRaw, &defaulted) != nil || defaulted.DueTimezone != "America/Los_Angeles" || defaulted.DueAt == nil || defaulted.DueAt.Format(time.RFC3339) != "2024-11-22T03:00:00Z" {
		t.Fatalf("defaulted task timezone = %s", defaultedRaw)
	}
}

func TestSpaceAgentMayParaphraseExplicitSharedMessage(t *testing.T) {
	if !api.TestingSpaceAgentSendIsGrounded("Tell everyone Melissa will wash the dishes tonight at 7 PM", "Melissa is handling the dishes by 7 PM.") {
		t.Fatal("explicit send request should allow a concise agent-authored paraphrase")
	}
	if api.TestingSpaceAgentSendIsGrounded("Add a task for Melissa", "Melissa is handling the dishes by 7 PM.") {
		t.Fatal("task request must not implicitly authorize a shared message")
	}
}

func TestSpaceAgentCreatesReadsAndUpdatesNativeNote(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Note Tool Owner", "note-tool-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Notes Space")
	if err != nil {
		t.Fatal(err)
	}
	createdRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Create a launch note", "notes.create", json.RawMessage(`{"title":"Launch plan","markdown":"# Launch\nInitial checklist"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created SpaceNote
	if err := json.Unmarshal(createdRaw, &created); err != nil || created.ID == "" {
		t.Fatalf("created note = %s, %v", createdRaw, err)
	}
	updatedRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Update the launch note", "notes.update", json.RawMessage(`{"id":"`+created.ID+`","title":"Launch plan","markdown":"# Launch\nReady for beta"}`))
	if err != nil {
		t.Fatal(err)
	}
	var updated SpaceNote
	if json.Unmarshal(updatedRaw, &updated) != nil || updated.ID != created.ID {
		t.Fatalf("updated note = %s", updatedRaw)
	}
	commands, err := database.PendingNoteControlCommands(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	foundReplace := false
	for _, command := range commands {
		if command.NoteID == created.ID && command.Command == "replace_markdown" {
			foundReplace = true
		}
	}
	if !foundReplace {
		t.Fatalf("missing durable replace_markdown command: %#v", commands)
	}
	applied, err := database.ApplySpaceNoteProjection(ctx, SpaceNoteProjection{
		NoteID: created.ID, Revision: created.CollaborationRevision + 1, Title: "Launch plan",
		Markdown: "# Launch\nReady for beta", PlainText: "Launch\nReady for beta",
	})
	if err != nil || !applied {
		t.Fatalf("room projection was not applied: applied=%v err=%v", applied, err)
	}
	searchRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Find launch notes", "notes.search", json.RawMessage(`{"query":"Ready for beta"}`))
	if err != nil || !strings.Contains(string(searchRaw), created.ID) {
		t.Fatalf("searched notes = %s, %v", searchRaw, err)
	}
}

func TestFamilySpaceResearchCanBeSavedAndPostedWithCitations(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Family Research Owner", "family-research-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Family Space")
	if err != nil {
		t.Fatal(err)
	}
	prompt := "Research summer camps in Pasadena, save the research, and post a cited summary to Family Space"
	sourceURL := "https://example.org/pasadena-camps"
	createdRaw, err := api.TestingExecuteSpaceConversationTool(
		ctx, database, owner.ID, space.ID, "", prompt, "notes.create",
		json.RawMessage(`{"title":"Pasadena summer camp research","markdown":"# Summer camps\n\nArt and science programs are available.\n\nSource: `+sourceURL+`"}`),
	)
	if err != nil || !strings.Contains(string(createdRaw), "Pasadena summer camp research") {
		t.Fatalf("saved research note = %s, %v", createdRaw, err)
	}
	summary := "Pasadena summer camps include art and science programs. Source: " + sourceURL
	if _, err := api.TestingExecuteSpaceConversationTool(
		ctx, database, owner.ID, space.ID, "", prompt, "messages.send",
		json.RawMessage(`{"message":"`+summary+`"}`),
	); err != nil {
		t.Fatalf("post cited summary: %v", err)
	}
	messages, err := database.SpaceMessages(ctx, owner.ID, space.ID, 0, 20)
	if err != nil || len(messages) != 1 || !strings.Contains(api.TestingSpansToPlainText(messages[0].Content), sourceURL) {
		t.Fatalf("Family Space cited summary = %#v, %v", messages, err)
	}
}

func TestSpaceAgentCreatesQueriesAndUpdatesNativeCalendarEvent(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Calendar Tool Owner", "calendar-tool-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Calendar Space")
	if err != nil {
		t.Fatal(err)
	}
	createdRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Schedule a calendar event", "calendar.create", json.RawMessage(`{"title":"Investor demo","startsAt":"2026-08-20T10:00:00-07:00","endsAt":"2026-08-20T10:30:00-07:00","timezone":"America/Los_Angeles"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created SpaceCalendarEvent
	if json.Unmarshal(createdRaw, &created) != nil || created.ID == "" || created.Timezone != "America/Los_Angeles" {
		t.Fatalf("created calendar event = %s", createdRaw)
	}
	from := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	to := from.Add(48 * time.Hour)
	events, err := database.SpaceCalendarEvents(ctx, owner.ID, space.ID, from, to)
	if err != nil || len(events) != 1 || events[0].ID != created.ID {
		t.Fatalf("queried calendar events = %#v, %v", events, err)
	}
	updatedRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Reschedule the meeting", "calendar.update", json.RawMessage(`{"id":"`+created.ID+`","startsAt":"2026-08-20T11:00:00-07:00","endsAt":"2026-08-20T11:30:00-07:00"}`))
	if err != nil {
		t.Fatal(err)
	}
	var updated SpaceCalendarEvent
	if json.Unmarshal(updatedRaw, &updated) != nil || updated.Version != created.Version+1 || updated.StartsAt.Equal(created.StartsAt) {
		t.Fatalf("updated calendar event = %s", updatedRaw)
	}
}

func TestSpaceAgentInterpretsCalendarTimestampInTheSuppliedTimezone(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Calendar Date Owner", "calendar-date-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Calendar Date Space")
	if err != nil {
		t.Fatal(err)
	}
	createdRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Schedule a calendar event", "calendar.create", json.RawMessage(`{"title":"Local date","startsAt":"2026-08-20T10:00:00","endsAt":"2026-08-20T10:30:00","timezone":"America/Los_Angeles"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created SpaceCalendarEvent
	if json.Unmarshal(createdRaw, &created) != nil || created.StartsAt.Format(time.RFC3339) != "2026-08-20T17:00:00Z" {
		t.Fatalf("local calendar timestamp = %s", createdRaw)
	}
}

func TestSpaceAgentCreatesReadsAndUpdatesRoadmap(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Roadmap Tool Owner", "roadmap-tool-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Roadmap Space")
	if err != nil {
		t.Fatal(err)
	}
	createdRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Create a product roadmap", "roadmaps.create", json.RawMessage(`{"name":"Beta launch","description":"Prepare the public beta"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created SpaceRoadmapSnapshot
	if json.Unmarshal(createdRaw, &created) != nil || created.Roadmap.ID == "" || len(created.Milestones) != 1 {
		t.Fatalf("created roadmap = %s", createdRaw)
	}
	queryRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Find the beta roadmap", "roadmaps.query", json.RawMessage(`{"query":"Beta"}`))
	if err != nil || !strings.Contains(string(queryRaw), created.Roadmap.ID) {
		t.Fatalf("queried roadmaps = %s, %v", queryRaw, err)
	}
	updatedRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Rename the roadmap", "roadmaps.update", json.RawMessage(`{"id":"`+created.Roadmap.ID+`","name":"Public beta launch"}`))
	if err != nil {
		t.Fatal(err)
	}
	var updated SpaceRoadmap
	if json.Unmarshal(updatedRaw, &updated) != nil || updated.Name != "Public beta launch" || updated.GraphVersion <= created.Roadmap.GraphVersion {
		t.Fatalf("updated roadmap = %s", updatedRaw)
	}
	readRaw, err := api.TestingExecuteSpaceConversationTool(ctx, database, owner.ID, space.ID, "", "Read the roadmap", "roadmaps.read", json.RawMessage(`{"id":"`+created.Roadmap.ID+`"}`))
	if err != nil || !strings.Contains(string(readRaw), "Public beta launch") {
		t.Fatalf("read roadmap = %s, %v", readRaw, err)
	}
}
