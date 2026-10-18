package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"github.com/kannachi323/misty/server/test/testkit"
)

func TestAgentItemToolsPostgres(t *testing.T) {
	if os.Getenv("TEST_DB_NAME") != "misty_agent_members_test" {
		t.Skip("requires isolated agent members test database")
	}
	database := testkit.OpenDatabase(t)
	ctx := t.Context()
	owner, _ := database.CreateUser("items-owner@example.test", "password123")
	teammate, _ := database.CreateUser("items-teammate@example.test", "password123")
	work, err := database.TestingCreateSpace(ctx, owner.ID, "Work")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')`, work.ID, teammate.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// This account lets agents act without approval cards.
	if err := database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_user_settings(user_id,app_actions_ask) VALUES($1,false) ON CONFLICT(user_id) DO UPDATE SET app_actions_ask=false`, owner.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	identity, _ := database.SavePersonalAgent(ctx, owner.ID, "", db.AgentProfileInput{Name: "Helper", Enabled: true})
	run, _ := database.CreateCreatorAgentRun(ctx, owner.ID, "", identity.ID, db.CreatorAgentRunInput{Instruction: "Tidy up"})
	service := &SpacesService{database: database}
	invocation := agenttools.Invocation{UserID: owner.ID, AgentID: identity.ID, RunID: run.ID}
	inSpace := invocation
	inSpace.SpaceID = work.ID
	call := func(handler agenttools.Handler, invocation agenttools.Invocation, arguments string) json.RawMessage {
		t.Helper()
		raw, err := handler(ctx, invocation, agent.ToolRequest{ID: "call-" + arguments, Arguments: json.RawMessage(arguments)})
		if err != nil {
			t.Fatalf("%s = %v", arguments, err)
		}
		return raw
	}
	rename := service.renameToolRegistration().Handler

	drawing, err := database.CreateSpaceDrawing(ctx, owner.ID, work.ID, "Sketch")
	if err != nil {
		t.Fatal(err)
	}
	if raw := call(rename, invocation, `{"kind":"drawing","id":"`+drawing.ID+`","name":"Floor plan"}`); !strings.Contains(string(raw), "Floor plan") {
		t.Fatalf("rename drawing = %s", raw)
	}
	thread, err := database.CreateSpaceConversation(ctx, owner.ID, work.ID, "Planning", []db.SpaceActorRef{{Kind: "person", UserID: teammate.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if raw := call(rename, invocation, `{"kind":"thread","id":"`+thread.ID+`","name":"Launch planning","space":"Work"}`); !strings.Contains(string(raw), "Launch planning") {
		t.Fatalf("rename thread = %s", raw)
	}

	// Deletes without an approval card for this account.
	call(service.executeItemDelete, invocation, `{"kind":"thread","id":"`+thread.ID+`","space":"`+work.ID+`"}`)
	if threads, _ := database.SpaceConversations(ctx, owner.ID, work.ID); len(threads) != 0 {
		t.Fatalf("thread survived delete: %#v", threads)
	}

	// Note tags in the Space.
	note, err := database.CreateSpaceNote(ctx, owner.ID, work.ID, "Launch notes")
	if err != nil {
		t.Fatal(err)
	}
	tags := noteTagsToolRegistration(database).Handler
	if raw := call(tags, inSpace, `{"id":"`+note.ID+`","tags":["launch","q4"]}`); !strings.Contains(string(raw), "q4") {
		t.Fatalf("note.tag = %s", raw)
	}

	// Resending an existing invitation refreshes its link.
	token, _ := security.GenerateSecureToken()
	invite, err := database.InviteToSpaceWithToken(ctx, owner.ID, work.ID, "friend@example.test", security.HashToken(token))
	if err != nil {
		t.Fatal(err)
	}
	if raw := call(service.executeSpaceAdmin, invocation, `{"action":"resend_invitation","space":"Work","invitation_id":"`+invite.ID+`"}`); !strings.Contains(string(raw), invite.ID) {
		t.Fatalf("resend invitation = %s", raw)
	}

	// Methods: save, update by id, and use.
	methods := map[string]agenttools.Handler{}
	for _, registration := range methodToolRegistrations(database, nil) {
		methods[registration.Descriptor.Name] = registration.Handler
	}
	var saved struct {
		Method struct{ ID string } `json:"method"`
	}
	_ = json.Unmarshal(call(methods["memory.workflow.save"], invocation, `{"title":"Standup","instructions":"Summarize yesterday for the team.","inputs":[{"key":"team","label":"Team","type":"text","required":true}]}`), &saved)
	if raw := call(methods["memory.workflow.save"], invocation, `{"id":"`+saved.Method.ID+`","title":"Daily standup","instructions":"Summarize yesterday."}`); !strings.Contains(string(raw), `"version":2`) {
		t.Fatalf("memory.workflow.save update = %s", raw)
	}
	if raw := call(methods["memory.workflow.use"], invocation, `{"method":"Daily standup"}`); !strings.Contains(string(raw), "Summarize yesterday") {
		t.Fatalf("memory.workflow.use after save = %s", raw)
	}
	var invalid agent.ErrInvalidRequest
	if _, err := methods["memory.workflow.save"](ctx, invocation, agent.ToolRequest{ID: "bad", Arguments: json.RawMessage(`{"id":"missing","title":"x","instructions":"y"}`)}); !errors.As(err, &invalid) {
		t.Fatalf("saving an unknown method = %v", err)
	}
}
