package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/db"
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
	alice, err := database.CreateUser("alice-member-request@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("bob-member-request@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, alice.ID, "Launch team")
	addTestSpaceMember(t, database, space.ID, bob.ID)
	bobAgent, err := database.SavePersonalAgent(ctx, bob.ID, "", AgentProfileInput{Name: "Researcher", Instructions: "Find facts.", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddSpaceAgent(ctx, bob.ID, space.ID, bobAgent.ID); err != nil {
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
	if err := database.SpaceTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')`, spaceID, userID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func setTestRunState(t *testing.T, database *Database, runID, state string) {
	t.Helper()
	if err := database.SpaceTx(context.Background(), func(tx *sql.Tx) error {
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

func TestAgentMemberRequestRunsAsTargetOwnerAtOnce(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	if _, err := f.database.AddSpaceAgent(ctx, f.alice.ID, f.space.ID, f.bobAgent.ID); !errors.Is(err, ErrPersonalAgentNotFound) {
		t.Fatalf("bringing someone else's agent = %v", err)
	}
	agents, err := f.database.SpaceAgents(ctx, f.alice.ID, f.space.ID)
	if err != nil || len(agents) != 1 || agents[0].AgentID != f.bobAgent.ID || agents[0].OwnerName != f.bob.Username {
		t.Fatalf("Space agents = %#v, %v", agents, err)
	}

	request, err := f.request("call-1")
	if err != nil {
		t.Fatal(err)
	}
	child, err := f.database.SpaceRun(ctx, f.bob.ID, request.ChildRunID)
	if err != nil {
		t.Fatal(err)
	}
	// The work acts as Bob's agent and is billed to Alice, who asked.
	if child.OwnerUserID != f.bob.ID || child.RequestingMemberID != f.bob.ID || child.ModelUserID != f.alice.ID || child.InitiatedByUserID != f.alice.ID {
		t.Fatalf("child ownership = owner %s requesting %s models %s initiated %s", child.OwnerUserID, child.RequestingMemberID, child.ModelUserID, child.InitiatedByUserID)
	}
	if child.SpaceID != f.space.ID || child.TriggerKind != "delegated" || child.ParentRunID != f.aliceRun.ID || child.DelegationDepth != 1 || child.AgentID != f.bobAgent.ID || child.InitialRunMode == "full" {
		t.Fatalf("child run = %#v", child)
	}
	// Nobody approves: the work is queued at once.
	if child.State != "queued" || testRunJobCount(t, f.database, child.ID) != 1 {
		t.Fatalf("request did not start at once: state %s", child.State)
	}
	replay, err := f.request("call-1")
	if err != nil || replay.ID != request.ID || !replay.Replay {
		t.Fatalf("replayed request = %#v, %v", replay, err)
	}
	found, err := f.database.AgentMemberRequestForRun(ctx, child.ID)
	if err != nil || found.ID != request.ID || found.RequesterName != f.alice.Username {
		t.Fatalf("request for run = %#v, %v", found, err)
	}
	if err := f.database.ValidateAgentMemberRequestRun(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.AgentMemberRequestForUser(ctx, f.bob.ID, request.ID); err != nil {
		t.Fatalf("target owner cannot see the request: %v", err)
	}
}

func TestAgentMemberRequestLimitsAndRemoval(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
	requests := []*AgentMemberRequest{}
	for _, key := range []string{"call-1", "call-2", "call-3"} {
		request, err := f.request(key)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request)
	}
	if _, err := f.request("call-4"); !errors.Is(err, ErrAgentRequestBusy) {
		t.Fatalf("request over the open limit = %v", err)
	}
	request := requests[0]
	setTestRunState(t, f.database, request.ChildRunID, "running")
	if _, err := f.database.CreateAgentMemberRequest(ctx, AgentMemberRequestInput{
		SpaceID: f.space.ID, RequesterUserID: f.bob.ID, RequesterAgentID: f.bobAgent.ID, RequesterRunID: request.ChildRunID,
		TargetAgentID: f.bobAgent.ID, Message: "Ask someone else", IdempotencyKey: "nested",
	}); !errors.Is(err, ErrAgentRequestChained) {
		t.Fatalf("delegated work requesting more work = %v", err)
	}

	// The Space's owner can take any agent out; work stops at its next tool call.
	if err := f.database.RemoveSpaceAgent(ctx, f.alice.ID, f.space.ID, f.bobAgent.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.database.ValidateAgentMemberRequestRun(ctx, request.ChildRunID); !errors.Is(err, ErrAgentNotInSpace) {
		t.Fatalf("agent kept working after it left the Space: %v", err)
	}
	if _, err := f.database.AddSpaceAgent(ctx, f.bob.ID, f.space.ID, f.bobAgent.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.database.ValidateAgentMemberRequestRun(ctx, request.ChildRunID); err != nil {
		t.Fatal(err)
	}
	if err := f.database.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM space_members WHERE space_id=$1 AND user_id=$2`, f.space.ID, f.bob.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.database.ValidateAgentMemberRequestRun(ctx, request.ChildRunID); !errors.Is(err, ErrAgentNotInSpace) {
		t.Fatalf("work continued after its owner left the Space: %v", err)
	}
	if agents, err := f.database.SpaceAgents(ctx, f.alice.ID, f.space.ID); err != nil || len(agents) != 0 {
		t.Fatalf("a former member's agent is still in the Space: %#v, %v", agents, err)
	}
	// It is still in Bob's personal Space.
	if ids, err := f.database.AgentSpaceIDs(ctx, f.bob.ID, f.bobAgent.ID); err != nil || len(ids) != 1 || ids[0] != personalSpace(t, f.database, ctx, f.bob.ID).ID {
		t.Fatalf("agent's Spaces after its owner left = %v, %v", ids, err)
	}
}

func TestCancellingRequesterStopsMemberWork(t *testing.T) {
	f := newMemberRequestFixture(t)
	ctx := context.Background()
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
	if err := f.database.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT payload FROM space_events WHERE space_id=$1 AND event_type='agent.request.created'`, f.space.ID).Scan(&payload)
	}); err != nil {
		t.Fatalf("request was not recorded in the Space activity: %v", err)
	}
}

func testRunJobCount(t *testing.T, database *Database, runID string) int {
	t.Helper()
	var count int
	if err := database.SpaceTx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM agent_run_jobs WHERE run_id=$1`, runID).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	return count
}
