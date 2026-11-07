package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/misty-org/misty-server/internal/platform/db"
)

const (
	bobAgentID = "agent-bob-on-his-computer"
)

// spaceAgentFixture is a shared Space, with one of Bob's agents listed in it.
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
	if _, err := database.AddSpaceAgent(ctx, bob.ID, space.ID, bobAgentID, "Researcher"); err != nil {
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

// A Space lists an agent by the id and name its owner's computer sent, with no
// agent row on the server.
func TestSpacesListAgentsByNameUnderTheirOwner(t *testing.T) {
	f := newSpaceAgentFixture(t)
	ctx := context.Background()
	agents, err := f.database.SpaceAgents(ctx, f.alice.ID, f.space.ID)
	if err != nil || len(agents) != 1 {
		t.Fatalf("Launch team's agents = %#v, %v", agents, err)
	}
	if got := agents[0]; got.AgentID != bobAgentID || got.AgentName != "Researcher" || got.OwnerUserID != f.bob.ID || got.OwnerName != f.bob.Username {
		t.Fatalf("listed agent = %#v", got)
	}
	// The same id again renames it; it never becomes a second row.
	if renamed, err := f.database.AddSpaceAgent(ctx, f.bob.ID, f.space.ID, bobAgentID, "Fact checker"); err != nil || renamed.AgentName != "Fact checker" {
		t.Fatalf("renamed = %#v, %v", renamed, err)
	}
	// Ids are the computer's own: two people's agents may share one.
	if _, err := f.database.AddSpaceAgent(ctx, f.alice.ID, f.space.ID, bobAgentID, "Planner"); err != nil {
		t.Fatal(err)
	}
	if agents, err = f.database.SpaceAgents(ctx, f.alice.ID, f.space.ID); err != nil || len(agents) != 2 {
		t.Fatalf("after Alice's agent = %#v, %v", agents, err)
	}
	if mine, err := f.database.SpaceAgents(ctx, f.alice.ID, ""); err != nil || len(mine) != 2 {
		t.Fatalf("/me/space-agents = %#v, %v", mine, err)
	}

	for _, bad := range [][2]string{{"", "Name"}, {"id", ""}, {"id", "   "}, {strings.Repeat("i", 201), "Name"}, {"id", strings.Repeat("n", 81)}} {
		if _, err := f.database.AddSpaceAgent(ctx, f.bob.ID, f.space.ID, bad[0], bad[1]); !errors.Is(err, ErrSpaceInvalid) {
			t.Fatalf("listing id %q name %q = %v", bad[0], bad[1], err)
		}
	}
	stranger, err := f.database.CreateUser("carol-space-agent@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.AddSpaceAgent(ctx, stranger.ID, f.space.ID, "agent-carol", "Writer"); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("listing an agent in a Space its owner isn't in = %v", err)
	}
	if _, err := f.database.SpaceAgents(ctx, stranger.ID, f.space.ID); err == nil {
		t.Fatal("someone outside the Space listed its agents")
	}
}

// Its owner or the Space's owner can take an agent out, and no one else.
func TestOnlyAnAgentsOwnerOrTheSpacesOwnerRemovesIt(t *testing.T) {
	f := newSpaceAgentFixture(t)
	ctx := context.Background()
	carol, err := f.database.CreateUser("carol-remove-agent@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	addTestSpaceMember(t, f.database, f.space.ID, carol.ID)
	if err := f.database.RemoveSpaceAgent(ctx, carol.ID, f.space.ID, bobAgentID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("another member removing Bob's agent = %v", err)
	}
	if err := f.database.RemoveSpaceAgent(ctx, f.bob.ID, f.space.ID, bobAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.AddSpaceAgent(ctx, f.bob.ID, f.space.ID, bobAgentID, "Researcher"); err != nil {
		t.Fatal(err)
	}
	if err := f.database.RemoveSpaceAgent(ctx, f.alice.ID, f.space.ID, bobAgentID); err != nil {
		t.Fatalf("the Space's owner removing a public agent = %v", err)
	}
	if agents, err := f.database.SpaceAgents(ctx, f.alice.ID, f.space.ID); err != nil || len(agents) != 0 {
		t.Fatalf("agents after removal = %#v, %v", agents, err)
	}
}

// A mention names an agent listed in the Space. The server records it and does
// nothing else: it starts on the sender's own computer.
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
	// A private agent can be mentioned by its owner and no one else.
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

func TestAgentArtifactsSaveToSpacesTheAgentIsIn(t *testing.T) {
	f := newSpaceAgentFixture(t)
	ctx := context.Background()
	digest := strings.Repeat("a", 64)
	provenance := &AgentArtifactProvenance{AgentID: bobAgentID, ConversationID: "conversation_private", RunID: "run_private", Kind: "download"}
	elsewhere := createTestSpace(t, f.database, ctx, f.bob.ID, "Side project")
	if _, err := f.database.CreateLibraryUploadWithProvenance(ctx, f.bob.ID, elsewhere.ID, "", "library",
		"report.pdf", "application/pdf", 32, digest, "library/elsewhere", "elsewhere-token", time.Now().Add(time.Hour), provenance); !errors.Is(err, ErrLibraryForbidden) {
		t.Fatalf("saving an artifact outside the agent's Space = %v", err)
	}
	if _, err := f.database.CreateLibraryUploadWithProvenance(ctx, f.alice.ID, f.space.ID, "", "library",
		"report.pdf", "application/pdf", 32, digest, "library/notmine", "notmine-token", time.Now().Add(time.Hour), provenance); !errors.Is(err, ErrLibraryForbidden) {
		t.Fatalf("saving an artifact for someone else's agent = %v", err)
	}
	upload, err := f.database.CreateLibraryUploadWithProvenance(ctx, f.bob.ID, f.space.ID, "", "library",
		"report.pdf", "application/pdf", 32, digest, "library/agentartifact", "artifact-token", time.Now().Add(time.Hour), provenance)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SetLibraryUploadState(ctx, f.bob.ID, f.space.ID, upload.ID, "artifact-token", "initiated", "uploaded_unverified"); err != nil {
		t.Fatal(err)
	}
	done, err := f.database.CompleteLibraryUpload(ctx, f.bob.ID, f.space.ID, upload.ID, "artifact-token", 32, digest, "application/pdf", nil)
	if err != nil || done.Item == nil {
		t.Fatalf("finished upload = %#v, %v", done, err)
	}

	mine, err := f.database.SpaceAgentArtifacts(ctx, f.bob.ID, f.space.ID, "", 0)
	if err != nil || len(mine) != 1 || mine[0].ItemID != done.Item.ID || mine[0].AgentName != "Researcher" || mine[0].Kind != "download" || mine[0].ConversationID != "conversation_private" {
		t.Fatalf("owner's view = %#v, %v", mine, err)
	}
	theirs, err := f.database.SpaceAgentArtifacts(ctx, f.alice.ID, f.space.ID, bobAgentID, 0)
	if err != nil || len(theirs) != 1 || theirs[0].OwnerName != f.bob.Username || theirs[0].ConversationID != "" || theirs[0].RunID != "" {
		t.Fatalf("another member's view = %#v, %v, want the item without Bob's private conversation", theirs, err)
	}
}
