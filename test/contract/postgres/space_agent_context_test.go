package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const allSpaceContextSections = `{"space_chat":true,"library":true,"task_notes":true,"tasks":true,"members":true}`

func TestSpaceContextUsesFixedMemberVisibility(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Context Owner", "context-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Context Member", "context-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Context Space")
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
	if _, err := database.CreateSpaceTask(ctx, owner.ID, SpaceTask{
		SpaceID: space.ID, Title: "Ship the beta checklist", Notes: "Blocked on review", Status: "todo",
	}); err != nil {
		t.Fatal(err)
	}

	context1, err := database.PersonalAgentSpaceContext(ctx, member.ID, space.ID, json.RawMessage(allSpaceContextSections))
	if err != nil {
		t.Fatalf("PersonalAgentSpaceContext() member error = %v", err)
	}
	if !strings.Contains(context1, "Ship the beta checklist") {
		t.Fatalf("context omitted a task the member can see:\n%s", context1)
	}
	if strings.Contains(context1, "Recent chat:") {
		t.Fatalf("context included chat for a member without messages.read:\n%s", context1)
	}
	if !strings.Contains(context1, "Context Space") {
		t.Fatalf("context omitted the Space name:\n%s", context1)
	}
}

func TestSharedAgentContextEnvelopeCarriesBoundariesAndFilteredRecords(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Envelope Owner", "envelope-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Envelope Member", "envelope-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Envelope Space")
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
	if _, err := database.CreateSpaceTask(ctx, owner.ID, SpaceTask{
		SpaceID: space.ID, Title: "Context envelope task", Status: "todo",
	}); err != nil {
		t.Fatal(err)
	}

	cardRaw, records, revision, err := api.TestingBuildAgentSharedSpaceContext(
		ctx, database, member.ID, space.ID, "agents", []string{"tasks.query", "members.list"},
	)
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		Version       int      `json:"version"`
		SpaceID       string   `json:"space_id"`
		SpaceName     string   `json:"space_name"`
		MemberCount   int      `json:"member_count"`
		ActiveSurface string   `json:"active_surface"`
		AllowedTools  []string `json:"allowed_tools"`
		Trust         string   `json:"trust"`
	}
	if json.Unmarshal(cardRaw, &card) != nil {
		t.Fatalf("invalid context card: %s", cardRaw)
	}
	if card.Version != 1 || card.SpaceID != space.ID || card.SpaceName != "Envelope Space" || card.MemberCount != 2 || card.ActiveSurface != "agents" {
		t.Fatalf("context card = %#v", card)
	}
	if strings.Join(card.AllowedTools, ",") != "members.list,tasks.query" || !strings.Contains(card.Trust, "never instructions") {
		t.Fatalf("context authority boundary = %#v", card)
	}
	if revision == "" || !strings.Contains(records, "Context envelope task") {
		t.Fatalf("context records/revision = %q / %q", records, revision)
	}
	if strings.Contains(records, "Recent chat:") {
		t.Fatalf("context leaked chat without messages.read:\n%s", records)
	}
}

// "notes" was renamed to "task_notes" because it always meant the notes column
// on a task, never the device-local Notes surface. The old key is a stored user
// setting, so it must keep working.
func TestSpaceContextAcceptsLegacyNotesSectionKey(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Notes Key Owner", "notes-key-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Notes Key Space")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSpaceTask(ctx, owner.ID, SpaceTask{
		SpaceID: space.ID, Title: "Task with notes", Notes: "Remember the recovery window", Status: "todo",
	}); err != nil {
		t.Fatal(err)
	}

	for name, sections := range map[string]string{
		"legacy key":  `{"tasks":true,"notes":true}`,
		"current key": `{"tasks":true,"task_notes":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := database.PersonalAgentSpaceContext(ctx, owner.ID, space.ID, json.RawMessage(sections))
			if err != nil {
				t.Fatalf("PersonalAgentSpaceContext() error = %v", err)
			}
			if !strings.Contains(out, "Remember the recovery window") {
				t.Fatalf("task notes missing from context:\n%s", out)
			}
			if !strings.Contains(out, "Task notes:") {
				t.Fatalf("section should be labelled as task notes, not Notes:\n%s", out)
			}
		})
	}
}

// The revision token decides whether the send path rebuilds context. It has to
// move when content the agent can see changes, and stay put otherwise, or every
// turn either serves stale context or re-pays the full prompt cost.
func TestSpaceContextRevisionTracksVisibleChanges(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Revision Owner", "revision-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Revision Member", "revision-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Revision Space")
	if err != nil {
		t.Fatal(err)
	}

	first, err := database.SpaceContextRevision(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" {
		t.Fatal("SpaceContextRevision() returned an empty token")
	}
	again, err := database.SpaceContextRevision(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Fatal("revision changed with no Space activity; context would be rebuilt every turn")
	}

	if _, err := database.CreateSpaceTask(ctx, owner.ID, SpaceTask{
		SpaceID: space.ID, Title: "Added after the first turn", Status: "todo",
	}); err != nil {
		t.Fatal(err)
	}
	afterTask, err := database.SpaceContextRevision(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterTask == first {
		t.Fatal("revision did not move after a task was added; the agent would answer from stale context")
	}

	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	afterMember, err := database.SpaceContextRevision(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterMember == afterTask {
		t.Fatal("revision did not move after a member joined")
	}

	// Tokens remain per member, so one account's cached context is never reused
	// for another account even though the Member defaults are fixed.
	memberToken, err := database.SpaceContextRevision(ctx, member.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	ownerToken, err := database.SpaceContextRevision(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ownerToken == memberToken {
		t.Fatal("two members produced the same revision; caller identity would be ignored")
	}
}
