package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type memberRequestFixture struct {
	database   *Database
	alice, bob *User
	space      *Space
	bobAgent   *AskIdentity
	aliceAgent *AskIdentity
	aliceRun   *SpaceRun
}

func newMemberRequestFixture(t *testing.T) memberRequestFixture {
	t.Helper()
	database := openTestDatabase(t)
	ctx := context.Background()
	alice, err := database.CreateUser("Alice", "alice-member-request@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("Bob", "bob-member-request@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, alice.ID, "Launch team")
	addTestSpaceMember(t, database, space.ID, bob.ID)
	bobAgent, err := database.SavePersonalAgent(ctx, bob.ID, "", AgentProfileInput{Name: "Researcher", Instructions: "Find facts.", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	aliceAgent, err := database.SavePersonalAgent(ctx, alice.ID, "", AgentProfileInput{Name: "Planner", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	aliceRun, err := database.CreateCreatorAgentRun(ctx, alice.ID, "", aliceAgent.ID, CreatorAgentRunInput{Instruction: "Plan the launch"})
	if err != nil {
		t.Fatal(err)
	}
	setTestRunState(t, database, aliceRun.ID, "running")
	return memberRequestFixture{database: database, alice: alice, bob: bob, space: space, bobAgent: bobAgent, aliceAgent: aliceAgent, aliceRun: aliceRun}
}

func addTestSpaceMember(t *testing.T, database *Database, spaceID, userID string) {
	t.Helper()
	if err := database.TestingSpaceTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')`, spaceID, userID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func setTestRunState(t *testing.T, database *Database, runID, state string) {
	t.Helper()
	if err := database.TestingSpaceTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE space_runs SET state=$2 WHERE id=$1`, runID, state)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func (f memberRequestFixture) request(key string) (*AgentMemberRequest, error) {
	return f.database.CreateAgentMemberRequest(context.Background(), AgentMemberRequestInput{
		SpaceID: f.space.ID, RequesterUserID: f.alice.ID, RequesterAgentID: f.aliceAgent.ID, RequesterRunID: f.aliceRun.ID,
		TargetAgentID: f.bobAgent.ID, Message: "Find the launch date", IdempotencyKey: key,
	})
}

func TestAgentMemberRequestRunsAsTargetOwnerAfterApproval(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	if _, err := f.request("call-1"); !errors.Is(err, ErrAgentListingUnavailable) {
		t.Fatalf("request to an unpublished agent = %v", err)
	}
	if _, err := f.database.SaveSpaceAgentListing(ctx, f.alice.ID, f.space.ID, f.bobAgent.ID, SpaceAgentListingInput{}); !errors.Is(err, ErrPersonalAgentNotFound) {
		t.Fatalf("publishing someone else's agent = %v", err)
	}
	if _, err := f.database.SaveSpaceAgentListing(ctx, f.bob.ID, f.space.ID, f.bobAgent.ID, SpaceAgentListingInput{Description: "Answers research questions"}); err != nil {
		t.Fatal(err)
	}
	listings, err := f.database.SpaceAgentListings(ctx, f.alice.ID, "")
	if err != nil || len(listings) != 1 || listings[0].AgentID != f.bobAgent.ID || listings[0].OwnerName != "Bob" {
		t.Fatalf("listings = %#v, %v", listings, err)
	}

	request, err := f.request("call-1")
	if err != nil {
		t.Fatal(err)
	}
	child, err := f.database.SpaceRun(ctx, f.bob.ID, request.ChildRunID)
	if err != nil {
		t.Fatal(err)
	}
	if child.OwnerUserID != f.bob.ID || child.RequestingMemberID != f.bob.ID || child.BillingUserID != f.bob.ID || child.InitiatedByUserID != f.alice.ID {
		t.Fatalf("child ownership = owner %s requesting %s billing %s initiated %s", child.OwnerUserID, child.RequestingMemberID, child.BillingUserID, child.InitiatedByUserID)
	}
	if child.SpaceID != f.space.ID || child.TriggerKind != "delegated" || child.ParentRunID != f.aliceRun.ID || child.DelegationDepth != 1 || child.AgentID != f.bobAgent.ID || child.InitialRunMode == "full" {
		t.Fatalf("child run = %#v", child)
	}
	// Bob's listing asks first by default, so nothing runs until Bob approves.
	if child.State != "queued" || request.Approval != "pending" || testRunJobCount(t, f.database, child.ID) != 0 {
		t.Fatalf("Ask-mode request started without approval: state %s", child.State)
	}
	pending, err := f.database.PendingAgentMemberRequests(ctx, f.bob.ID)
	if err != nil || len(pending) != 1 || pending[0].ID != request.ID {
		t.Fatalf("Bob's pending requests = %#v, %v", pending, err)
	}
	if _, err := f.database.DecideAgentMemberRequest(ctx, f.alice.ID, request.ID, true); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("the requester approved their own request: %v", err)
	}
	approved, err := f.database.DecideAgentMemberRequest(ctx, f.bob.ID, request.ID, true)
	if err != nil || approved.RunState != "queued" || testRunJobCount(t, f.database, child.ID) != 1 {
		t.Fatalf("approved request = %#v, %v", approved, err)
	}
	if _, err := f.database.DecideAgentMemberRequest(ctx, f.bob.ID, request.ID, false); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("deciding twice = %v", err)
	}
	replay, err := f.request("call-1")
	if err != nil || replay.ID != request.ID || !replay.Replay {
		t.Fatalf("replayed request = %#v, %v", replay, err)
	}
	// Bob pays for his agent's work under its own command; Alice pays only
	// for her own run.
	command, err := f.database.BillingCommandForRun(ctx, f.bob.ID, child.ID)
	if err != nil || command != "agent-runtime:"+child.ID {
		t.Fatalf("target owner billing command = %q, %v", command, err)
	}
	if _, err := f.database.BillingCommandForRun(ctx, f.alice.ID, child.ID); err == nil {
		t.Fatal("the requester was billed for the target agent's work")
	}
	found, err := f.database.AgentMemberRequestForRun(ctx, child.ID)
	if err != nil || found.ID != request.ID || found.RequesterName != "Alice" {
		t.Fatalf("request for run = %#v, %v", found, err)
	}
	if err := f.database.ValidateAgentMemberRequestRun(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.AgentMemberRequestForUser(ctx, f.bob.ID, request.ID); err != nil {
		t.Fatalf("target owner cannot see the request: %v", err)
	}
}

func TestAgentMemberRequestLimitsAndRevocation(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	if _, err := f.database.SaveSpaceAgentListing(ctx, f.bob.ID, f.space.ID, f.bobAgent.ID, SpaceAgentListingInput{MaxOpenRequests: 1}); err != nil {
		t.Fatal(err)
	}
	request, err := f.request("call-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.request("call-2"); !errors.Is(err, ErrAgentRequestBusy) {
		t.Fatalf("request over the open limit = %v", err)
	}
	setTestRunState(t, f.database, request.ChildRunID, "running")
	if _, err := f.database.CreateAgentMemberRequest(ctx, AgentMemberRequestInput{
		SpaceID: f.space.ID, RequesterUserID: f.bob.ID, RequesterAgentID: f.bobAgent.ID, RequesterRunID: request.ChildRunID,
		TargetAgentID: f.bobAgent.ID, Message: "Ask someone else", IdempotencyKey: "nested",
	}); !errors.Is(err, ErrAgentRequestChained) {
		t.Fatalf("delegated work requesting more work = %v", err)
	}

	if err := f.database.DeleteSpaceAgentListing(ctx, f.bob.ID, f.space.ID, f.bobAgent.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.database.ValidateAgentMemberRequestRun(ctx, request.ChildRunID); !errors.Is(err, ErrAgentListingUnavailable) {
		t.Fatalf("unpublished agent kept working: %v", err)
	}
	if _, err := f.database.SaveSpaceAgentListing(ctx, f.bob.ID, f.space.ID, f.bobAgent.ID, SpaceAgentListingInput{AcceptPolicy: "off"}); err != nil {
		t.Fatal(err)
	}
	if err := f.database.ValidateAgentMemberRequestRun(ctx, request.ChildRunID); !errors.Is(err, ErrAgentListingUnavailable) {
		t.Fatalf("listing set to off kept working: %v", err)
	}
	if _, err := f.database.SaveSpaceAgentListing(ctx, f.bob.ID, f.space.ID, f.bobAgent.ID, SpaceAgentListingInput{AcceptPolicy: "auto", MaxOpenRequests: 3}); err != nil {
		t.Fatal(err)
	}
	if err := f.database.ValidateAgentMemberRequestRun(ctx, request.ChildRunID); err != nil {
		t.Fatal(err)
	}
	if err := f.database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM space_members WHERE space_id=$1 AND user_id=$2`, f.space.ID, f.bob.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.database.ValidateAgentMemberRequestRun(ctx, request.ChildRunID); !errors.Is(err, ErrAgentListingUnavailable) {
		t.Fatalf("work continued after its owner left the Space: %v", err)
	}
	if listings, err := f.database.SpaceAgentListings(ctx, f.alice.ID, f.space.ID); err != nil || len(listings) != 0 {
		t.Fatalf("a former member's agent is still listed: %#v, %v", listings, err)
	}
}

func TestCancellingRequesterStopsMemberWork(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	if _, err := f.database.SaveSpaceAgentListing(ctx, f.bob.ID, f.space.ID, f.bobAgent.ID, SpaceAgentListingInput{}); err != nil {
		t.Fatal(err)
	}
	request, err := f.request("call-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.CancelPersonalAgentTaskRunForOwner(ctx, f.alice.ID, f.aliceRun.ID); err != nil {
		t.Fatal(err)
	}
	child, err := f.database.SpaceRun(ctx, f.bob.ID, request.ChildRunID)
	if err != nil || child.State != "canceled" {
		t.Fatalf("member work after the requester stopped = %#v, %v", child, err)
	}
	var payload json.RawMessage
	if err := f.database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT payload FROM space_events WHERE space_id=$1 AND event_type='agent.request.created'`, f.space.ID).Scan(&payload)
	}); err != nil {
		t.Fatalf("request was not recorded in the Space activity: %v", err)
	}
}

func testRunJobCount(t *testing.T, database *Database, runID string) int {
	t.Helper()
	var count int
	if err := database.TestingSpaceTx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM agent_run_jobs WHERE run_id=$1`, runID).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAgentMemberRequestAutoModeDeclineAndExpiry(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	if _, err := f.database.SaveSpaceAgentListing(ctx, f.bob.ID, f.space.ID, f.bobAgent.ID, SpaceAgentListingInput{MaxOpenRequests: 1}); err != nil {
		t.Fatal(err)
	}
	declined, err := f.request("ask-1")
	if err != nil {
		t.Fatal(err)
	}
	if declined, err = f.database.DecideAgentMemberRequest(ctx, f.bob.ID, declined.ID, false); err != nil || declined.RunState != "rejected" || declined.RunErrorMessage == "" {
		t.Fatalf("declined request = %#v, %v", declined, err)
	}
	stale, err := f.request("ask-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.request("ask-3"); !errors.Is(err, ErrAgentRequestBusy) {
		t.Fatalf("an unanswered request did not hold the open slot: %v", err)
	}
	if err := f.database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agent_member_requests SET created_at=NOW()-INTERVAL '25 hours' WHERE id=$1`, stale.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// A listing that starts at once queues work immediately, and the expired
	// request frees its slot.
	if _, err := f.database.SaveSpaceAgentListing(ctx, f.bob.ID, f.space.ID, f.bobAgent.ID, SpaceAgentListingInput{AcceptPolicy: "auto", MaxOpenRequests: 1}); err != nil {
		t.Fatal(err)
	}
	auto, err := f.request("auto-1")
	if err != nil || auto.RunState != "queued" || testRunJobCount(t, f.database, auto.ChildRunID) != 1 {
		t.Fatalf("Auto-mode request = %#v, %v", auto, err)
	}
	expired, err := f.database.AgentMemberRequestForUser(ctx, f.alice.ID, stale.ID)
	if err != nil || expired.RunState != "rejected" {
		t.Fatalf("stale request = %#v, %v", expired, err)
	}
}
