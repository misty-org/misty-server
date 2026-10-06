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
	owner, _ := database.CreateUser("Owner", "items-owner@example.test", "password123")
	teammate, _ := database.CreateUser("Teammate", "items-teammate@example.test", "password123")
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
	album, err := database.CreateLibraryAlbum(ctx, owner.ID, work.ID, "Old album", "")
	if err != nil {
		t.Fatal(err)
	}
	roadmap, err := database.CreateSpaceRoadmap(ctx, owner.ID, work.ID, "Old roadmap", "")
	if err != nil {
		t.Fatal(err)
	}
	for kind, id := range map[string]string{"thread": thread.ID, "album": album.ID, "roadmap": roadmap.Roadmap.ID} {
		call(service.executeItemDelete, invocation, `{"kind":"`+kind+`","id":"`+id+`","space":"`+work.ID+`"}`)
	}
	if _, err := database.LibraryAlbum(ctx, owner.ID, work.ID, album.ID); err == nil {
		t.Fatal("album survived delete")
	}
	if threads, _ := database.SpaceConversations(ctx, owner.ID, work.ID); len(threads) != 0 {
		t.Fatalf("thread survived delete: %#v", threads)
	}

	// Roadmap canvas: a risk that blocks a goal, then unlinked and archived.
	canvasRoadmap, _ := database.CreateSpaceRoadmap(ctx, owner.ID, work.ID, "Launch", "")
	firstMilestone := canvasRoadmap.Milestones[0].ID
	var goal struct {
		Goal struct{ ID string } `json:"goal"`
	}
	_ = json.Unmarshal(call(roadmapPlanToolRegistration(database).Handler, inSpace, `{"action":"add_goal","roadmap_id":"`+canvasRoadmap.Roadmap.ID+`","milestone_id":"`+firstMilestone+`","title":"Ship beta"}`), &goal)
	canvas := roadmapCanvasToolRegistration(database).Handler
	var node struct {
		Node struct{ ID string } `json:"node"`
	}
	_ = json.Unmarshal(call(canvas, inSpace, `{"action":"add_node","roadmap_id":"`+canvasRoadmap.Roadmap.ID+`","node_kind":"risk","title":"Vendor delay"}`), &node)
	var edge struct {
		Edge struct{ ID string } `json:"edge"`
	}
	_ = json.Unmarshal(call(canvas, inSpace, `{"action":"link","roadmap_id":"`+canvasRoadmap.Roadmap.ID+`","source":{"kind":"node","id":"`+node.Node.ID+`"},"target":{"kind":"goal","id":"`+goal.Goal.ID+`"},"edge_type":"blocks"}`), &edge)
	if edge.Edge.ID == "" {
		t.Fatal("risk was not linked to the goal")
	}
	if _, err := canvas(ctx, inSpace, agent.ToolRequest{ID: "bad-edge", Arguments: json.RawMessage(`{"action":"link","roadmap_id":"` + canvasRoadmap.Roadmap.ID + `","source":{"kind":"node","id":"` + node.Node.ID + `"},"target":{"kind":"goal","id":"` + goal.Goal.ID + `"},"edge_type":"measures"}`)}); err == nil {
		t.Fatal("a risk was allowed to measure a goal")
	}
	call(canvas, inSpace, `{"action":"unlink","roadmap_id":"`+canvasRoadmap.Roadmap.ID+`","edge_id":"`+edge.Edge.ID+`"}`)
	call(canvas, inSpace, `{"action":"archive_node","roadmap_id":"`+canvasRoadmap.Roadmap.ID+`","node_id":"`+node.Node.ID+`"}`)
	if after, _ := database.SpaceRoadmap(ctx, owner.ID, work.ID, canvasRoadmap.Roadmap.ID); len(after.Nodes) != 0 || len(after.Edges) != 0 {
		t.Fatalf("canvas after cleanup = %d nodes, %d edges", len(after.Nodes), len(after.Edges))
	}

	// Note tags in the Space.
	note, err := database.CreateSpaceNote(ctx, owner.ID, work.ID, "Launch notes")
	if err != nil {
		t.Fatal(err)
	}
	tags := noteTagsToolRegistration(database).Handler
	if raw := call(tags, inSpace, `{"id":"`+note.ID+`","tags":["launch","q4"]}`); !strings.Contains(string(raw), "q4") {
		t.Fatalf("notes.tags = %s", raw)
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
	for _, registration := range methodToolRegistrations(database) {
		methods[registration.Descriptor.Name] = registration.Handler
	}
	var saved struct {
		Method struct{ ID string } `json:"method"`
	}
	_ = json.Unmarshal(call(methods["methods.save"], invocation, `{"title":"Standup","instructions":"Summarize yesterday for the team.","inputs":[{"key":"team","label":"Team","type":"text","required":true}]}`), &saved)
	if raw := call(methods["methods.save"], invocation, `{"id":"`+saved.Method.ID+`","title":"Daily standup","instructions":"Summarize yesterday."}`); !strings.Contains(string(raw), `"version":2`) {
		t.Fatalf("methods.save update = %s", raw)
	}
	if raw := call(methods["methods.use"], invocation, `{"method":"Daily standup"}`); !strings.Contains(string(raw), "Summarize yesterday") {
		t.Fatalf("methods.use after save = %s", raw)
	}
	var invalid agent.ErrInvalidRequest
	if _, err := methods["methods.save"](ctx, invocation, agent.ToolRequest{ID: "bad", Arguments: json.RawMessage(`{"id":"missing","title":"x","instructions":"y"}`)}); !errors.As(err, &invalid) {
		t.Fatalf("saving an unknown method = %v", err)
	}
}
