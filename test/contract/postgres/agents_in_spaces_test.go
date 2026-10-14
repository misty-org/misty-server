package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAgentsLiveInExactlyOneSpace(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	alice, err := database.CreateUser("Alice Lee", "alice-agent-home@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("Bob", "bob-agent-home@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	home := personalSpace(t, database, ctx, alice.ID)
	if home.Name != "Alice's Space" {
		t.Fatalf("personal Space name = %q", home.Name)
	}
	misty, err := database.EnsureAskIdentity(ctx, alice.ID, "google/gemini-2.5-flash-lite")
	if err != nil || misty.SpaceID != home.ID {
		t.Fatalf("built-in agent = %#v, %v, want it in the personal Space", misty, err)
	}
	writer, err := database.SavePersonalAgent(ctx, alice.ID, "", AgentProfileInput{Name: "Writer", Enabled: true})
	if err != nil || writer.SpaceID != home.ID {
		t.Fatalf("new agent = %#v, %v, want the personal Space by default", writer, err)
	}
	listings, err := database.SpaceAgentListings(ctx, alice.ID, home.ID)
	if err != nil || len(listings) != 2 {
		t.Fatalf("agent members of the personal Space = %#v, %v", listings, err)
	}
	for _, listing := range listings {
		if listing.AcceptPolicy != "ask" || listing.OwnerUserID != alice.ID {
			t.Fatalf("agent member = %#v, want Alice's, asking first", listing)
		}
	}

	shared := createTestSpace(t, database, ctx, bob.ID, "Studio")
	move := AgentProfileInput{Name: "Writer", Enabled: true, Version: writer.Version, SpaceID: shared.ID}
	if _, err := database.SavePersonalAgent(ctx, alice.ID, writer.ID, move); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("moving into a Space the owner isn't in = %v", err)
	}
	invite, err := database.TestingInviteToSpace(ctx, bob.ID, shared.ID, alice.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, alice.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	moved, err := database.SavePersonalAgent(ctx, alice.ID, writer.ID, move)
	if err != nil || moved.SpaceID != shared.ID {
		t.Fatalf("moved agent = %#v, %v", moved, err)
	}
	listings, err = database.SpaceAgentListings(ctx, bob.ID, shared.ID)
	if err != nil || len(listings) != 1 || listings[0].AgentID != writer.ID || listings[0].OwnerName != "Alice Lee" || listings[0].AcceptPolicy != "ask" {
		t.Fatalf("Studio's agent members = %#v, %v", listings, err)
	}
	if listings, err = database.SpaceAgentListings(ctx, alice.ID, home.ID); err != nil || len(listings) != 1 {
		t.Fatalf("the moved agent is still a member of its old Space: %#v, %v", listings, err)
	}
	pinned := AgentProfileInput{Name: "Misty", Enabled: true, Version: misty.Version, SpaceID: shared.ID}
	if _, err := database.SavePersonalAgent(ctx, alice.ID, misty.ID, pinned); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("moving the built-in agent = %v", err)
	}

	if err := database.DeleteSpace(ctx, bob.ID, shared.ID, shared.Name); err != nil {
		t.Fatal(err)
	}
	back, err := database.AskIdentityByID(ctx, alice.ID, writer.ID)
	if err != nil || back.SpaceID != home.ID {
		t.Fatalf("agent after its Space was deleted = %#v, %v, want the personal Space", back, err)
	}
}

func TestMentioningAnAgentInSpaceChatAsksItsOwner(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	mention := []MessageSpan{{Type: "text", Text: "Can you check this? "}, {Type: "mention", AgentID: f.bobAgent.ID, Label: "Researcher"}}
	message, mentioned, err := f.database.TestingCreateSpaceMessage(ctx, f.alice.ID, f.space.ID, mention, nil)
	if err != nil || len(mentioned) != 1 || mentioned[0] != f.bobAgent.ID {
		t.Fatalf("message mentioning another member's agent = %v, %v", mentioned, err)
	}
	ask := AgentMemberRequestInput{SpaceID: f.space.ID, RequesterUserID: f.alice.ID, RequesterRunID: MentionRequester(message.ID),
		TargetAgentID: f.bobAgent.ID, Message: "Can you check this? @Researcher", IdempotencyKey: f.bobAgent.ID}
	request, err := f.database.CreateAgentMemberRequest(ctx, ask)
	if err != nil || request.Approval != "pending" || request.SourceMessageID != message.ID {
		t.Fatalf("mention request = %#v, %v", request, err)
	}
	child, err := f.database.SpaceRun(ctx, f.bob.ID, request.ChildRunID)
	if err != nil || child.BillingUserID != f.alice.ID || child.OwnerUserID != f.bob.ID {
		t.Fatalf("mention work = %#v, %v, want Bob's agent billed to Alice", child, err)
	}
	if replay, err := f.database.CreateAgentMemberRequest(ctx, ask); err != nil || replay.ID != request.ID {
		t.Fatalf("the same mention asked twice = %#v, %v", replay, err)
	}
	reply, err := f.database.CreateAgentMemberRequestMessage(ctx, request, "agent_member_reply", "The launch is on the 14th.")
	if err != nil || reply.SenderAgentID != f.bobAgent.ID || reply.ReplyToMessageID != message.ID {
		t.Fatalf("reply = %#v, %v, want Bob's agent answering under the message", reply, err)
	}

	plain, _, err := f.database.TestingCreateSpaceMessage(ctx, f.alice.ID, f.space.ID, []MessageSpan{{Type: "text", Text: "No mention here"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ask.RequesterRunID = MentionRequester(plain.ID)
	if _, err := f.database.CreateAgentMemberRequest(ctx, ask); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("a request naming a message without the mention = %v", err)
	}

	// Owners never wait on themselves.
	own, _, err := f.database.TestingCreateSpaceMessage(ctx, f.bob.ID, f.space.ID, mention, nil)
	if err != nil {
		t.Fatal(err)
	}
	self, err := f.database.CreateAgentMemberRequest(ctx, AgentMemberRequestInput{SpaceID: f.space.ID, RequesterUserID: f.bob.ID,
		RequesterRunID: MentionRequester(own.ID), TargetAgentID: f.bobAgent.ID, Message: "Check this", IdempotencyKey: f.bobAgent.ID})
	if err != nil || self.Approval != "not_needed" {
		t.Fatalf("owner's own mention = %#v, %v", self, err)
	}
}

func TestAgentArtifactsSaveToTheAgentsSpace(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	digest := strings.Repeat("a", 64)
	provenance := &AgentArtifactProvenance{AgentID: f.bobAgent.ID, ConversationID: "conversation_private", RunID: "run_private", Kind: "download"}
	if _, err := f.database.CreateLibraryUploadWithProvenance(ctx, f.bob.ID, personalSpace(t, f.database, ctx, f.bob.ID).ID, "", "library",
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
	if err != nil || len(mine) != 1 || mine[0].ItemID != done.Item.ID || mine[0].AgentName != "Researcher" || mine[0].Kind != "download" || mine[0].RunID != "run_private" {
		t.Fatalf("owner's view = %#v, %v", mine, err)
	}
	theirs, err := f.database.SpaceAgentArtifacts(ctx, f.alice.ID, f.space.ID, f.bobAgent.ID, 0)
	if err != nil || len(theirs) != 1 || theirs[0].OwnerName != "Bob" || theirs[0].ConversationID != "" || theirs[0].RunID != "" {
		t.Fatalf("another member's view = %#v, %v, want the item without Bob's private conversation", theirs, err)
	}
}

// Rebuilds an account without a personal Space and an agent without a Space
// inside one rolled-back transaction, then runs the migration's backfill.
func TestAgentsInSpacesMigrationMovesEveryAgentHome(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	kept, err := database.CreateUser("Kept Home", "agents-migration-kept@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := database.CreateUser("Nora Fresh", "agents-migration-fresh@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	keptHome := personalSpace(t, database, ctx, kept.ID)
	keptAgent, err := database.SavePersonalAgent(ctx, kept.ID, "", AgentProfileInput{Name: "Kept", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	freshAgent, err := database.SavePersonalAgent(ctx, fresh.ID, "", AgentProfileInput{Name: "Fresh", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	oldHome := personalSpace(t, database, ctx, fresh.ID)
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../internal/platform/postgres/migrations/20271012020000_agents_in_spaces.sql"))
	if err != nil {
		t.Fatal(err)
	}
	backfill := []string{}
	for _, statement := range strings.Split(strings.Split(string(raw), "-- +goose Down")[0], ";\n") {
		trimmed := strings.TrimSpace(statement)
		if strings.HasPrefix(trimmed, "SELECT misty_ensure_personal_space(id) FROM users") || strings.HasPrefix(trimmed, "UPDATE misty_ask_identities a SET space_id=s.id FROM spaces s") {
			backfill = append(backfill, trimmed)
		}
	}
	if len(backfill) != 2 {
		t.Fatalf("backfill statements = %q", backfill)
	}
	rollback := errors.New("rollback migration fixture")
	err = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		exec := func(query string, args ...any) error { _, err := tx.ExecContext(ctx, query, args...); return err }
		// Nora's account predates personal Spaces: her only Space is not the
		// default, and her agent has no Space yet.
		for _, query := range []string{
			`ALTER TABLE spaces DISABLE TRIGGER spaces_protect_default_update`,
			`ALTER TABLE misty_ask_identities DISABLE TRIGGER misty_agent_space_guard`,
			`ALTER TABLE misty_ask_identities DROP CONSTRAINT misty_ask_identities_space_required`,
		} {
			if err := exec(query); err != nil {
				return err
			}
		}
		if err := exec(`UPDATE spaces SET is_default=FALSE,name='Old project' WHERE id=$1`, oldHome.ID); err != nil {
			return err
		}
		if err := exec(`UPDATE misty_ask_identities SET space_id=NULL WHERE id IN ($1,$2)`, keptAgent.ID, freshAgent.ID); err != nil {
			return err
		}
		for _, query := range backfill {
			if err := exec(query); err != nil {
				return err
			}
		}
		var keptSpace, freshSpace, freshName string
		if err := tx.QueryRowContext(ctx, `SELECT space_id FROM misty_ask_identities WHERE id=$1`, keptAgent.ID).Scan(&keptSpace); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT a.space_id,s.name FROM misty_ask_identities a JOIN spaces s ON s.id=a.space_id WHERE a.id=$1`, freshAgent.ID).Scan(&freshSpace, &freshName); err != nil {
			return err
		}
		if keptSpace != keptHome.ID {
			t.Errorf("agent of an account with a default Space went to %s, want %s", keptSpace, keptHome.ID)
		}
		if freshSpace == oldHome.ID || freshName != "Nora's Space" {
			t.Errorf("agent of an account without a personal Space went to %s (%q), want a new Nora's Space", freshSpace, freshName)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
}
