package db

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"github.com/kannachi323/misty/server/test/testkit"
)

func newRefreshSession(t *testing.T, username string) (*Database, string, string) {
	t.Helper()
	database := testkit.OpenDatabase(t)
	user, err := database.CreateUserWithUsername("Ada", username, username+"@example.com", "password-long-enough")
	if err != nil {
		t.Fatal(err)
	}
	sid := security.HashToken("sid-" + username)
	if err := database.CreateRefreshSession(context.Background(), sid, security.HashToken("old"), user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return database, sid, user.ID
}

func rotate(t *testing.T, database *Database, sid, from, to, userID string) bool {
	t.Helper()
	ok, err := database.RotateRefreshSession(context.Background(), sid, security.HashToken(from), security.HashToken(to), userID)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestRefreshRotationRevokesFamilyOnReplay(t *testing.T) {
	database, sid, userID := newRefreshSession(t, "refresh_ada")
	if !rotate(t, database, sid, "old", "new", userID) {
		t.Fatal("first rotation rejected")
	}
	if !rotate(t, database, sid, "new", "next", userID) {
		t.Fatal("second rotation rejected")
	}
	// "old" is two rotations back, so it is a replay even inside the grace window.
	if rotate(t, database, sid, "old", "replay", userID) {
		t.Fatal("replay accepted")
	}
	if rotate(t, database, sid, "next", "after", userID) {
		t.Fatal("revoked family accepted")
	}
}

func TestRefreshRotationAcceptsRetryAfterLostResponse(t *testing.T) {
	database, sid, userID := newRefreshSession(t, "refresh_retry")
	if !rotate(t, database, sid, "old", "lost", userID) {
		t.Fatal("first rotation rejected")
	}
	// The client never received "lost" and retries with the token it still holds.
	if !rotate(t, database, sid, "old", "retry", userID) {
		t.Fatal("retry within the grace window rejected")
	}
	if !rotate(t, database, sid, "retry", "next", userID) {
		t.Fatal("token issued by the retry rejected")
	}
}

func TestRefreshRotationGraceExpires(t *testing.T) {
	database, sid, userID := newRefreshSession(t, "refresh_grace")
	if !rotate(t, database, sid, "old", "new", userID) {
		t.Fatal("first rotation rejected")
	}
	err := database.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE sessions SET refresh_rotated_at=$2 WHERE token_hash=$1`,
			sid, time.Now().Add(-RefreshRotationGrace-time.Minute))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if rotate(t, database, sid, "old", "late", userID) {
		t.Fatal("reuse after the grace window accepted")
	}
	if rotate(t, database, sid, "new", "next", userID) {
		t.Fatal("late reuse did not revoke the family")
	}
}

func TestConcurrentRefreshWithOneTokenDoesNotRevokeSession(t *testing.T) {
	database, sid, userID := newRefreshSession(t, "refresh_race")
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for _, next := range []string{"next-a", "next-b"} {
		wg.Add(1)
		go func(next string) {
			defer wg.Done()
			ok, err := database.RotateRefreshSession(context.Background(), sid, security.HashToken("old"), security.HashToken(next), userID)
			if err != nil {
				t.Error(err)
			}
			results <- ok
		}(next)
	}
	wg.Wait()
	close(results)
	for ok := range results {
		if !ok {
			t.Fatal("a concurrent refresh inside the grace window revoked the session")
		}
	}
}
