package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	. "github.com/misty-org/misty-server/internal/platform/db"
)

const (
	bobAgentID = "agent-bob-on-his-computer"
)

type spaceAgentFixture struct {
	database   *Database
	alice, bob *User
	space      *Space
}

func newSpaceAgentFixture(t *testing.T) spaceAgentFixture {
	t.Helper()
	database := openTestDatabase(t)
	ctx := context.Background()
	alice, err := database.CreateUser("alice-space-agent@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("bob-space-agent@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, alice.ID, "Launch team")
	addTestSpaceMember(t, database, space.ID, bob.ID)
	if _, err := addTestSpaceAgent(ctx, database, bob.ID, space.ID, bobAgentID, "Researcher"); err != nil {
		t.Fatal(err)
	}
	return spaceAgentFixture{database: database, alice: alice, bob: bob, space: space}
}

func addTestSpaceMember(t *testing.T, database *Database, spaceID, userID string) {
	t.Helper()
	if err := database.SpaceTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')`, spaceID, userID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func addTestSpaceAgent(ctx context.Context, database *Database, userID, spaceID, agentID, name string) (*SpaceAgent, error) {
	def := SpaceAgentDefinition{Name: name, EngineID: "claude", Instructions: "Check the sources."}
	if agents, err := database.SpaceAgents(ctx, userID, spaceID); err == nil {
		for _, agent := range agents {
			if agent.AgentID == agentID {
				def.Revision = agent.Revision
			}
		}
	}
	return database.PutSpaceAgent(ctx, userID, spaceID, agentID, def)
}

func TestSpacesKeepTheAgentsDefinedInThem(t *testing.T) {
	f := newSpaceAgentFixture(t)
	ctx := context.Background()
	agents, err := f.database.SpaceAgents(ctx, f.alice.ID, f.space.ID)
	if err != nil || len(agents) != 1 {
		t.Fatalf("Launch team's agents = %#v, %v", agents, err)
	}
	got := agents[0]
	if got.AgentID != bobAgentID || got.AgentName != "Researcher" || got.OwnerUserID != f.bob.ID || got.OwnerName != f.bob.Username ||
		got.EngineID != "claude" || got.Instructions != "Check the sources." || got.Revision != 1 || string(got.Avatar) != "{}" {
		t.Fatalf("defined agent = %#v", got)
	}
	if !got.CanEdit {
		t.Fatal("the Space's owner can't edit an agent in it")
	}
	renamed, err := f.database.PutSpaceAgent(ctx, f.bob.ID, f.space.ID, bobAgentID, SpaceAgentDefinition{
		Name: "Fact checker", EngineID: "codex", VoiceID: "calm-1", Avatar: []byte(`{"cloudVariant":"mint"}`), Revision: 1,
	})
	if err != nil || renamed.AgentName != "Fact checker" || renamed.EngineID != "codex" || renamed.VoiceID != "calm-1" || renamed.Revision != 2 {
		t.Fatalf("renamed = %#v, %v", renamed, err)
	}
	if _, err := f.database.PutSpaceAgent(ctx, f.bob.ID, f.space.ID, bobAgentID, SpaceAgentDefinition{Name: "Stale", Revision: 1}); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("an edit from an old revision = %v", err)
	}
	if _, err := f.database.PutSpaceAgent(ctx, f.alice.ID, f.space.ID, bobAgentID, SpaceAgentDefinition{Name: "Planner", Revision: 2}); err != nil {
		t.Fatalf("the Space's owner editing = %v", err)
	}
	if _, err := addTestSpaceAgent(ctx, f.database, f.alice.ID, f.space.ID, "agent-alice", "Writer"); err != nil {
		t.Fatal(err)
	}
	if agents, err = f.database.SpaceAgents(ctx, f.bob.ID, f.space.ID); err != nil || len(agents) != 2 {
		t.Fatalf("after Alice's agent = %#v, %v", agents, err)
	}
	if mine, err := f.database.SpaceAgents(ctx, f.alice.ID, ""); err != nil || len(mine) < 2 {
		t.Fatalf("/me/space-agents = %#v, %v", mine, err)
	}

	for _, bad := range []SpaceAgentDefinition{
		{Name: ""}, {Name: "   "}, {Name: strings.Repeat("n", 81)}, {Name: "Voice", VoiceID: "Not A Voice"},
		{Name: "Engine", EngineID: "../claude"}, {Name: "Avatar", Avatar: []byte(`[1]`)},
		{Name: "Nested", Avatar: []byte(`{"key":{"secret":"x"}}`)}, {Name: "Long", Instructions: strings.Repeat("i", 16001)},
	} {
		if _, err := f.database.PutSpaceAgent(ctx, f.bob.ID, f.space.ID, "agent-bad", bad); !errors.Is(err, ErrSpaceInvalid) {
			t.Fatalf("defining %#v = %v", bad, err)
		}
	}
	if _, err := f.database.PutSpaceAgent(ctx, f.bob.ID, f.space.ID, strings.Repeat("i", 201), SpaceAgentDefinition{Name: "Name"}); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("a long id = %v", err)
	}
	stranger, err := f.database.CreateUser("carol-space-agent@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := addTestSpaceAgent(ctx, f.database, stranger.ID, f.space.ID, "agent-carol", "Writer"); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("defining an agent in a Space its maker isn't in = %v", err)
	}
	if _, err := f.database.SpaceAgents(ctx, stranger.ID, f.space.ID); err == nil {
		t.Fatal("someone outside the Space listed its agents")
	}
}

func TestOnlyAnAgentsMakerTheOwnerOrAModeratorChangesIt(t *testing.T) {
	f := newSpaceAgentFixture(t)
	ctx := context.Background()
	carol, err := f.database.CreateUser("carol-remove-agent@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	addTestSpaceMember(t, f.database, f.space.ID, carol.ID)
	agents, err := f.database.SpaceAgents(ctx, carol.ID, f.space.ID)
	if err != nil || len(agents) != 1 || agents[0].CanEdit {
		t.Fatalf("a member's view = %#v, %v", agents, err)
	}
	if _, err := f.database.PutSpaceAgent(ctx, carol.ID, f.space.ID, bobAgentID, SpaceAgentDefinition{Name: "Mine now", Revision: 1}); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("another member editing Bob's agent = %v", err)
	}
	if err := f.database.RemoveSpaceAgent(ctx, carol.ID, f.space.ID, bobAgentID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("another member removing Bob's agent = %v", err)
	}
	name, permissions := "Moderator", []string{PermissionMessagesManage}
	role, err := f.database.CreateSpaceRole(ctx, f.alice.ID, f.space.ID, SpaceRoleInput{Name: &name, Permissions: &permissions})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SetSpaceMemberRoles(ctx, f.alice.ID, f.space.ID, carol.ID, []string{role.ID}); err != nil {
		t.Fatal(err)
	}
	if edited, err := f.database.PutSpaceAgent(ctx, carol.ID, f.space.ID, bobAgentID, SpaceAgentDefinition{Name: "Moderated", Revision: 1}); err != nil || edited.OwnerUserID != f.bob.ID || !edited.CanEdit {
		t.Fatalf("a moderator editing Bob's agent = %#v, %v", edited, err)
	}
	if err := f.database.RemoveSpaceAgent(ctx, f.bob.ID, f.space.ID, bobAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := addTestSpaceAgent(ctx, f.database, f.bob.ID, f.space.ID, bobAgentID, "Researcher"); err != nil {
		t.Fatal(err)
	}
	if err := f.database.RemoveSpaceAgent(ctx, f.alice.ID, f.space.ID, bobAgentID); err != nil {
		t.Fatalf("the Space's owner removing an agent = %v", err)
	}
	if agents, err := f.database.SpaceAgents(ctx, f.alice.ID, f.space.ID); err != nil || len(agents) != 0 {
		t.Fatalf("agents after removal = %#v, %v", agents, err)
	}
}

func TestAListingFromBeforeDefinitionsIsOnlyItsMakersToFill(t *testing.T) {
	f := newSpaceAgentFixture(t)
	ctx := context.Background()
	if err := f.database.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO space_agents(space_id,agent_id,owner_user_id,agent_name,revision) VALUES($1,'agent-legacy',$2,'Old',0)`, f.space.ID, f.bob.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.PutSpaceAgent(ctx, f.alice.ID, f.space.ID, "agent-legacy", SpaceAgentDefinition{Name: "Taken"}); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("the owner filling Bob's old listing = %v", err)
	}
	filled, err := f.database.PutSpaceAgent(ctx, f.bob.ID, f.space.ID, "agent-legacy", SpaceAgentDefinition{Name: "Old", EngineID: "claude"})
	if err != nil || filled.Revision != 1 || filled.EngineID != "claude" {
		t.Fatalf("Bob filling his old listing = %#v, %v", filled, err)
	}
}

func TestMentionsNameAgentsListedInTheSpace(t *testing.T) {
	f := newSpaceAgentFixture(t)
	ctx := context.Background()
	mention := func(agentID string) []MessageSpan {
		return []MessageSpan{{Type: "text", Text: "Can you check this? "}, {Type: "mention", AgentID: agentID, Label: "Researcher"}}
	}
	_, mentioned, err := f.database.TestingCreateSpaceMessage(ctx, f.alice.ID, f.space.ID, mention(bobAgentID), nil)
	if err != nil || len(mentioned) != 1 || mentioned[0] != bobAgentID {
		t.Fatalf("mention of a listed agent = %v, %v", mentioned, err)
	}
	if _, _, err := f.database.TestingCreateSpaceMessage(ctx, f.alice.ID, f.space.ID, mention("agent-nobody-listed"), nil); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("mention of an unlisted agent = %v", err)
	}
	if err := f.database.SetSpaceItemVisibility(ctx, f.bob.ID, f.space.ID, "agent", bobAgentID, VisibilityPrivate); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.database.TestingCreateSpaceMessage(ctx, f.alice.ID, f.space.ID, mention(bobAgentID), nil); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("mention of someone else's private agent = %v", err)
	}
	if _, _, err := f.database.TestingCreateSpaceMessage(ctx, f.bob.ID, f.space.ID, mention(bobAgentID), nil); err != nil {
		t.Fatalf("mention of your own private agent = %v", err)
	}
}
