package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/test/testkit"
)

// decideNextApproval waits for the approval card a tool call opens and answers it.
func decideNextApproval(t *testing.T, database *db.Database, userID, subject, state string) {
	t.Helper()
	ctx := context.Background()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		var id string
		err := database.SpaceTx(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, `SELECT id FROM agent_app_requests WHERE owner_user_id=$1 AND subject=$2 AND state='pending'`, userID, subject).Scan(&id)
		})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := database.DecideAgentAppRequest(ctx, userID, id, state); err != nil {
			t.Error(err)
		}
		return
	}
	t.Errorf("no approval card for %s", subject)
}

func TestAgentCatalogToolsPostgres(t *testing.T) {
	if os.Getenv("TEST_DB_NAME") != "misty_agent_members_test" {
		t.Skip("requires isolated agent members test database")
	}
	database := testkit.OpenDatabase(t)
	ctx := t.Context()
	owner, _ := database.CreateUser("catalog-owner@example.test", "password123")
	work, err := database.TestingCreateSpace(ctx, owner.ID, "Work")
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := database.SavePersonalAgent(ctx, owner.ID, "", db.AgentProfileInput{Name: "Helper", Enabled: true})
	run, err := database.CreateCreatorAgentRun(ctx, owner.ID, "", identity.ID, db.CreatorAgentRunInput{Instruction: "Tidy up"})
	if err != nil {
		t.Fatal(err)
	}
	service := &SpacesService{database: database}
	invocation := agenttools.Invocation{UserID: owner.ID, AgentID: identity.ID, RunID: run.ID}
	call := func(handler agenttools.Handler, arguments string) (json.RawMessage, error) {
		return handler(ctx, invocation, agent.ToolRequest{ID: "call-" + arguments, Arguments: json.RawMessage(arguments)})
	}

	search := searchAllToolRegistration(database).Handler
	if raw, err := call(search, `{"query":"launch"}`); err != nil || !strings.Contains(string(raw), `"results"`) {
		t.Fatalf("search.all = %s, %v", raw, err)
	}
	var invalid agent.ErrInvalidRequest
	if _, err := call(search, `{"query":"launch","space":"Nowhere"}`); !errors.As(err, &invalid) {
		t.Fatalf("search.all in an unknown Space = %v", err)
	}

	// Workflow schedules: setting and pausing need no approval; removing asks first.
	workflow, err := database.SaveAgentMethod(ctx, owner.ID, db.AgentMethod{AgentID: identity.ID, Kind: "workflow", Enabled: true, Definition: db.AgentMethodDefinition{Title: "Weekly review", Instructions: "Summarize my week", Inputs: []db.AgentMethodInput{}, Target: "cloud", RequiredTools: []string{}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := call(service.executeWorkflowSchedule, `{"workflow_id":"`+workflow.ID+`","timezone":"America/Los_Angeles","rules":[{"frequency":"weekly","weekdays":[1],"times":["09:00"]},{"frequency":"monthly","nth_weekdays":[{"nth":1,"weekday":1}],"times":["08:00"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Schedule db.WorkflowSchedule `json:"schedule"`
	}
	if json.Unmarshal(raw, &created) != nil || created.Schedule.ID == "" || created.Schedule.NextRunAt == nil {
		t.Fatalf("memory.workflow.schedule = %s", raw)
	}
	if _, err := call(service.executeWorkflowSchedule, `{"workflow_id":"`+workflow.ID+`","timezone":"UTC","rules":[{"frequency":"weekly","times":["25:99"]}]}`); !errors.As(err, &invalid) {
		t.Fatalf("invalid schedule = %v", err)
	}
	if raw, err := call(service.executeWorkflowSchedule, `{"workflow_id":"`+workflow.ID+`","timezone":"UTC","rules":[{"frequency":"daily","times":["09:00"]}],"enabled":false}`); err != nil || !strings.Contains(string(raw), `"enabled":false`) {
		t.Fatalf("memory.workflow.schedule pause = %s, %v", raw, err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := call(service.executeWorkflowUnschedule, `{"workflow_id":"`+workflow.ID+`"}`)
		done <- err
	}()
	decideNextApproval(t, database, owner.ID, "misty.workflows.unschedule", "approved")
	if err := <-done; err != nil {
		t.Fatalf("approved unschedule = %v", err)
	}
	if items, err := database.WorkflowSchedules(ctx, owner.ID); err != nil || len(items) != 0 {
		t.Fatalf("schedules after removal = %#v, %v", items, err)
	}

	// Space administration: rename directly; a declined invite changes nothing.
	if raw, err := call(service.executeSpaceAdmin, `{"action":"rename","space":"Work","name":"Work HQ"}`); err != nil || !strings.Contains(string(raw), "Work HQ") {
		t.Fatalf("rename = %s, %v", raw, err)
	}
	go func() {
		_, err := call(service.executeSpaceAdmin, `{"action":"invite","space":"`+work.ID+`","email":"friend@example.test"}`)
		done <- err
	}()
	decideNextApproval(t, database, owner.ID, "misty.spaces.invite", "declined")
	if err := <-done; !errors.As(err, &invalid) || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("declined invite = %v", err)
	}
	if invitations, err := database.PendingSpaceInvitations(ctx, owner.ID, work.ID); err != nil || len(invitations) != 0 {
		t.Fatalf("declined invite was sent: %#v, %v", invitations, err)
	}
	if raw, err := call(service.executeSpaceAdmin, `{"action":"create","name":"Side project"}`); err != nil || !strings.Contains(string(raw), "Side project") {
		t.Fatalf("create = %s, %v", raw, err)
	}

	// Threads: list, post as the agent, and read the post back.
	teammate, _ := database.CreateUser("catalog-teammate@example.test", "password123")
	if err := database.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')`, work.ID, teammate.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	thread, err := database.CreateSpaceConversation(ctx, owner.ID, work.ID, "Planning", []db.SpaceActorRef{{Kind: "person", UserID: teammate.ID}})
	if err != nil {
		t.Fatal(err)
	}
	threads := map[string]agenttools.Handler{}
	for _, registration := range threadToolRegistrations(database) {
		threads[registration.Descriptor.Name] = registration.Handler
	}
	spaceInvocation := invocation
	spaceInvocation.SpaceID = work.ID
	inSpace := func(name, arguments string) (json.RawMessage, error) {
		return threads[name](ctx, spaceInvocation, agent.ToolRequest{ID: "thread-" + name, Arguments: json.RawMessage(arguments)})
	}
	if raw, err := inSpace("thread.create", `{"title":"Retro","member_ids":["`+teammate.ID+`"]}`); err != nil || !strings.Contains(string(raw), "Retro") {
		t.Fatalf("thread.create = %s, %v", raw, err)
	}
	if raw, err := inSpace("thread.list", `{}`); err != nil || !strings.Contains(string(raw), thread.ID) {
		t.Fatalf("thread.list = %s, %v", raw, err)
	}
	if _, err := inSpace("thread.post", `{"thread_id":"`+thread.ID+`","message":"Kickoff is Monday"}`); err != nil {
		t.Fatalf("thread.post = %v", err)
	}
	if raw, err := inSpace("thread.read", `{"thread_id":"`+thread.ID+`"}`); err != nil || !strings.Contains(string(raw), "Kickoff is Monday") || !strings.Contains(string(raw), `"author_kind":"agent"`) {
		t.Fatalf("thread.read = %s, %v", raw, err)
	}
	if _, err := inSpace("thread.read", `{"thread_id":"missing"}`); !errors.As(err, &invalid) {
		t.Fatalf("unknown thread = %v", err)
	}

	// Methods: list the agent's saved workflow and render it with inputs.
	if _, err := database.SaveAgentMethod(ctx, owner.ID, db.AgentMethod{AgentID: identity.ID, Kind: "workflow", Enabled: true,
		Definition: db.AgentMethodDefinition{Title: "Weekly brief", Instructions: "Summarize the week for the team.", Target: "cloud",
			Inputs: []db.AgentMethodInput{{Key: "team", Label: "Team", Type: "text", Required: true}}, RequiredTools: []string{}}}, 0); err != nil {
		t.Fatal(err)
	}
	methods := map[string]agenttools.Handler{}
	for _, registration := range methodToolRegistrations(database, nil) {
		methods[registration.Descriptor.Name] = registration.Handler
	}
	if raw, err := call(methods["memory.workflow.list"], `{}`); err != nil || !strings.Contains(string(raw), "Weekly brief") {
		t.Fatalf("memory.workflow.list = %s, %v", raw, err)
	}
	if raw, err := call(methods["memory.workflow.use"], `{"method":"Weekly brief","inputs":{"team":"Launch"}}`); err != nil || !strings.Contains(string(raw), "Summarize the week") || !strings.Contains(string(raw), "Launch") {
		t.Fatalf("memory.workflow.use = %s, %v", raw, err)
	}
	if _, err := call(methods["memory.workflow.use"], `{"method":"Weekly brief","inputs":{}}`); !errors.As(err, &invalid) {
		t.Fatalf("missing required input = %v", err)
	}

	// item.delete removes a note only after the user approves.
	note, err := database.CreateSpaceNote(ctx, owner.ID, work.ID, "Old notes")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, err := call(service.executeItemDelete, `{"kind":"note","id":"`+note.ID+`","space":"`+work.ID+`"}`)
		done <- err
	}()
	decideNextApproval(t, database, owner.ID, "misty.items.delete", "approved")
	if err := <-done; err != nil {
		t.Fatalf("approved note delete = %v", err)
	}
	if notes, _ := database.AccessibleSpaceNotes(ctx, owner.ID, work.ID); len(notes) != 0 {
		t.Fatalf("note still active after delete: %#v", notes)
	}
}
