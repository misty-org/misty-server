package api

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/kannachi323/misty/server/test/testkit"
)

// testDatabaseLockID matches the advisory lock ID used by the db and
// integration packages' own test helpers, so this package's tests don't run
// concurrently with theirs against the same shared test database (they
// truncate tables between tests; racing that would be flaky by construction).
const testDatabaseLockID int64 = 621042

func openPresenceTestDatabase(t *testing.T) *db.Database {
	t.Helper()

	testkit.ApplyDatabaseEnvironment(t)

	database := &db.Database{}
	if err := database.Start(); err != nil {
		t.Fatalf("database.Start() error = %v", err)
	}
	lockConnection, err := database.Conn.Conn(t.Context())
	if err != nil {
		database.Stop()
		t.Fatalf("reserve test database lock connection: %v", err)
	}
	if _, err := lockConnection.ExecContext(t.Context(), `SELECT pg_advisory_lock($1)`, testDatabaseLockID); err != nil {
		_ = lockConnection.Close()
		database.Stop()
		t.Fatalf("acquire test database lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = lockConnection.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, testDatabaseLockID)
		_ = lockConnection.Close()
		database.Stop()
	})
	return database
}

func TestRealtimePresenceIsDisabledForCanonicalMisty(t *testing.T) {
	database := openPresenceTestDatabase(t)
	operator, err := database.CreateUser("Presence Operator", uniqueTestEmail("operator"), "password123")
	if err != nil {
		t.Fatalf("CreateUser(operator) error = %v", err)
	}
	member, err := database.CreateUser("Presence Customer", uniqueTestEmail("customer"), "password123")
	if err != nil {
		t.Fatalf("CreateUser(member) error = %v", err)
	}
	if err := database.ConfigureCanonicalMistySpace(t.Context(), operator.ID); err != nil {
		t.Fatalf("ConfigureCanonicalMistySpace() error = %v", err)
	}
	spaces, err := database.ListSpaces(t.Context(), member.ID)
	if err != nil {
		t.Fatalf("ListSpaces(member) error = %v", err)
	}
	var mistySpaceID string
	for _, space := range spaces {
		if space.Kind == "misty" {
			mistySpaceID = space.ID
			break
		}
	}
	if mistySpaceID == "" {
		t.Fatal("member did not receive canonical Misty Space")
	}

	service := NewRealtimeService(database, "")
	operatorClient := newTestRealtimeClient(operator.ID)
	memberClient := newTestRealtimeClient(member.ID)
	service.TestingSetViewing(operatorClient, mistySpaceID, true)
	service.TestingSetViewing(memberClient, mistySpaceID, true)
	assertNoMessage(t, operatorClient, "operator in canonical Misty")
	assertNoMessage(t, memberClient, "customer in canonical Misty")
	service.TestingMu.RLock()
	viewerCount := len(service.TestingViewers[mistySpaceID])
	service.TestingMu.RUnlock()
	if viewerCount != 0 {
		t.Fatalf("canonical Misty tracked %d Space-wide viewers, want none", viewerCount)
	}
}

// uniqueTestEmail avoids colliding with rows left behind by a previous run of
// this test against the same persistent database (this package doesn't
// truncate tables between runs the way db/integration's helpers do).
func uniqueTestEmail(label string) string {
	return fmt.Sprintf("presence-%s-%d@example.com", label, time.Now().UnixNano())
}

func newTestRealtimeClient(userID string) *TestingRealtimeClient {
	return &TestingRealtimeClient{TestingUserID: userID, TestingSend: make(chan []byte, 10), TestingDone: make(chan struct{})}
}

type presenceMessage struct {
	Type    string                  `json:"type"`
	SpaceID string                  `json:"space_id"`
	Viewers []TestingPresenceViewer `json:"viewers"`
}

func viewerStatus(viewers []TestingPresenceViewer, userID string) (active bool, found bool) {
	for _, viewer := range viewers {
		if viewer.UserID == userID {
			return viewer.Active, true
		}
	}
	return false, false
}

func readPresenceMessage(t *testing.T, client *TestingRealtimeClient) presenceMessage {
	t.Helper()
	select {
	case payload := <-client.TestingSend:
		var msg presenceMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			t.Fatalf("unmarshal presence message: %v", err)
		}
		return msg
	default:
		t.Fatal("expected a presence message, got none")
		return presenceMessage{}
	}
}

func assertNoMessage(t *testing.T, client *TestingRealtimeClient, context string) {
	t.Helper()
	select {
	case payload := <-client.TestingSend:
		t.Fatalf("%s: expected no message, got %s", context, payload)
	default:
	}
}

// TestRealtimePresenceRejectsNonMembers guards the security property behind
// the "active users" capsule: a client cannot make itself appear as
// "viewing" a Space it does not belong to, and doing so must not leak its
// presence (or anyone else's) to that Space's real members.
func TestRealtimePresenceRejectsNonMembers(t *testing.T) {
	database := openPresenceTestDatabase(t)
	ctx := context.Background()

	owner, err := database.CreateUser("Presence Owner", uniqueTestEmail("owner"), "password123")
	if err != nil {
		t.Fatalf("CreateUser(owner) error = %v", err)
	}
	outsider, err := database.CreateUser("Presence Outsider", uniqueTestEmail("outsider"), "password123")
	if err != nil {
		t.Fatalf("CreateUser(outsider) error = %v", err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Presence")
	if err != nil {
		t.Fatalf("CreateSpace(owner) error = %v", err)
	}

	service := NewRealtimeService(database, "")
	ownerClient := newTestRealtimeClient(owner.ID)
	outsiderClient := newTestRealtimeClient(outsider.ID)

	service.TestingSetViewing(ownerClient, space.ID, true)
	msg := readPresenceMessage(t, ownerClient)
	if msg.Type != "presence" || msg.SpaceID != space.ID || len(msg.Viewers) != 1 || msg.Viewers[0].UserID != owner.ID || !msg.Viewers[0].Active {
		t.Fatalf("owner presence message = %#v, want solo active owner", msg)
	}

	// The outsider is not a member of this Space. Claiming to view it must be
	// silently rejected: no broadcast to the real member, and no confirmation
	// back to the outsider either.
	service.TestingSetViewing(outsiderClient, space.ID, true)
	assertNoMessage(t, ownerClient, "owner after outsider's rejected viewing claim")
	assertNoMessage(t, outsiderClient, "outsider after rejected viewing claim")

	service.TestingMu.RLock()
	_, outsiderTrackedAsViewer := service.TestingViewers[space.ID][outsiderClient]
	service.TestingMu.RUnlock()
	if outsiderTrackedAsViewer {
		t.Fatal("outsider must not be tracked as a viewer of a Space it does not belong to")
	}

	// A real invited member joining is broadcast correctly to the owner.
	member, err := database.CreateUser("Presence Member", uniqueTestEmail("member"), "password123")
	if err != nil {
		t.Fatalf("CreateUser(member) error = %v", err)
	}
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatalf("InviteToSpace() error = %v", err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatalf("RespondToSpaceInvite() error = %v", err)
	}
	memberClient := newTestRealtimeClient(member.ID)
	service.TestingSetViewing(memberClient, space.ID, true)

	ownerMsg := readPresenceMessage(t, ownerClient)
	ownerActive, ownerFound := viewerStatus(ownerMsg.Viewers, owner.ID)
	memberActive, memberFound := viewerStatus(ownerMsg.Viewers, member.ID)
	if len(ownerMsg.Viewers) != 2 || !ownerFound || !ownerActive || !memberFound || !memberActive {
		t.Fatalf("owner presence after member joined = %#v, want owner and member both active", ownerMsg)
	}
	memberMsg := readPresenceMessage(t, memberClient)
	if len(memberMsg.Viewers) != 2 {
		t.Fatalf("member presence on join = %#v, want owner and member", memberMsg)
	}

	// The member going idle (still viewing, but Misty isn't in their focus)
	// keeps them in the viewer list but flips their status.
	service.TestingSetViewing(memberClient, space.ID, false)
	ownerMsgAfterIdle := readPresenceMessage(t, ownerClient)
	memberActiveAfterIdle, memberFoundAfterIdle := viewerStatus(ownerMsgAfterIdle.Viewers, member.ID)
	if len(ownerMsgAfterIdle.Viewers) != 2 || !memberFoundAfterIdle || memberActiveAfterIdle {
		t.Fatalf("owner presence after member went idle = %#v, want member present but idle", ownerMsgAfterIdle)
	}

	// The member navigating away (viewing "") removes them and notifies the
	// remaining viewer.
	service.TestingSetViewing(memberClient, "", false)
	ownerMsgAfterLeave := readPresenceMessage(t, ownerClient)
	if len(ownerMsgAfterLeave.Viewers) != 1 || ownerMsgAfterLeave.Viewers[0].UserID != owner.ID {
		t.Fatalf("owner presence after member left = %#v, want solo owner", ownerMsgAfterLeave)
	}

	// Disconnecting (unregister) also cleans up viewer state and notifies
	// anyone still watching.
	secondMemberClient := newTestRealtimeClient(member.ID)
	service.TestingSetViewing(secondMemberClient, space.ID, true)
	readPresenceMessage(t, ownerClient) // drain the join broadcast
	readPresenceMessage(t, secondMemberClient)

	service.TestingUnregister(secondMemberClient)
	ownerMsgAfterDisconnect := readPresenceMessage(t, ownerClient)
	if len(ownerMsgAfterDisconnect.Viewers) != 1 || ownerMsgAfterDisconnect.Viewers[0].UserID != owner.ID {
		t.Fatalf("owner presence after disconnect = %#v, want solo owner", ownerMsgAfterDisconnect)
	}

	service.TestingMu.RLock()
	_, spaceStillTracked := service.TestingViewers[space.ID][secondMemberClient]
	service.TestingMu.RUnlock()
	if spaceStillTracked {
		t.Fatal("disconnected client must be removed from the viewers map")
	}
}
