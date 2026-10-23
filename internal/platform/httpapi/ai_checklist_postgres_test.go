package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/test/testkit"
)

func TestLiveChecklistPostgres(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := t.Context()
	owner, _ := database.CreateUser("checklist-owner@example.test", "password123")
	conversation := "conversation_" + uuid.NewString()
	if err := database.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO misty_ask_conversations(id,user_id) VALUES($1,$2)`, conversation, owner.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	countGrants := func() int {
		count := 0
		_ = database.SpaceTx(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_grants WHERE grantor_user_id=$1`, owner.ID).Scan(&count)
		})
		return count
	}

	// Approving a plan grants nothing: no remembered approval appears.
	plan, err := database.ProposeAgentPlan(ctx, owner.ID, conversation, "run_plan", "agent", db.AgentPlanPayload{
		Title: "Tidy", Summary: "Tidy the inbox", Steps: []db.AgentPlanStep{{ID: "s1", Title: "Read inbox", Risk: "read"}, {ID: "s2", Title: "Archive old mail", Risk: "write"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := database.ApproveAgentPlan(ctx, owner.ID, plan.ID, plan.Version)
	if err != nil {
		t.Fatal(err)
	}
	if countGrants() != 0 {
		t.Fatal("approving a plan must not create grants")
	}

	// The first update seeds the checklist from the approved plan.
	service := &SpacesService{database: database}
	record := &db.AIInvocationRecord{ID: "invocation_checklist_parent", UserID: owner.ID, ConversationID: conversation, RequestPayload: json.RawMessage(`{}`)}
	state := collaborationState{mode: db.ConversationModeAct, plan: approved}
	handler := onlyChecklistHandler(service.checklistToolRegistrations(record, state), t)
	call := func(arguments string) (json.RawMessage, error) {
		return handler(ctx, agenttools.Invocation{UserID: owner.ID}, agent.ToolRequest{ID: "c", Arguments: json.RawMessage(arguments)})
	}
	if _, err := call(`{"items":[{"id":"s1","status":"in_progress"}]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := call(`{"items":[{"id":"s1","status":"done"},{"id":"s3","title":"Report back","status":"pending","delegated":true}]}`); err != nil {
		t.Fatal(err)
	}
	var invalid agent.ErrInvalidRequest
	if _, err := call(`{"items":[{"id":"s9","status":"done"}]}`); !errors.As(err, &invalid) {
		t.Fatalf("a new step without a title = %v", err)
	}
	if _, err := call(`{"items":[{"id":"s1","status":"finished"}]}`); !errors.As(err, &invalid) {
		t.Fatalf("an unknown status = %v", err)
	}

	// A delegated run's checklist nests under its parent's.
	child := &db.AIInvocationRecord{ID: "invocation_checklist_child", UserID: owner.ID, RequestPayload: json.RawMessage(`{"delegated_from":"invocation_checklist_parent"}`)}
	if _, err := database.SaveAgentChecklist(ctx, owner.ID, child.ID, conversation, record.ID, nil, []db.ChecklistItem{{ID: "c1", Title: "Edit tests", Status: "in_progress"}}, false); err != nil {
		t.Fatal(err)
	}
	lists, err := database.ConversationChecklists(ctx, owner.ID, conversation)
	if err != nil || len(lists) != 2 {
		t.Fatalf("checklists = %+v, %v", lists, err)
	}
	parent := lists[0]
	if parent.InvocationID != record.ID || parent.Version != 2 || len(parent.Items) != 3 {
		t.Fatalf("parent checklist = %+v", parent)
	}
	if parent.Items[0].Status != "done" || parent.Items[1].Title != "Archive old mail" || !parent.Items[2].Delegated {
		t.Fatalf("merged items = %+v", parent.Items)
	}
	if lists[1].ParentInvocationID != record.ID {
		t.Fatalf("child checklist = %+v", lists[1])
	}
	if countGrants() != 0 {
		t.Fatal("checklists never create grants")
	}
}

// onlyChecklistHandler returns the only registration's handler.
func onlyChecklistHandler(registrations []agenttools.Registration, t *testing.T) agenttools.Handler {
	t.Helper()
	if len(registrations) != 1 || registrations[0].Descriptor.Name != collaborationChecklist {
		t.Fatalf("registrations = %+v", registrations)
	}
	return registrations[0].Handler
}

func TestChecklistIsNotOfferedInPlan(t *testing.T) {
	service := &SpacesService{}
	record := &db.AIInvocationRecord{ID: "invocation_plan", UserID: "u", ConversationID: "c", RequestPayload: json.RawMessage(`{}`)}
	if got := service.checklistToolRegistrations(record, collaborationState{mode: db.ConversationModePlan}); len(got) != 0 {
		t.Fatal("Plan mode is read-only research; the checklist starts once work does")
	}
}
