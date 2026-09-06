package db

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentListSpacesNeverCrossesAccounts guards the server side of the
// account-switch race investigated on the client: even under heavy concurrent
// load from two different accounts hitting the same read path at once, each
// request must only ever see its own account's data. Each request is
// independently authenticated and scoped by userID, so this should hold by
// construction — this test exists to keep it that way as ListSpaces evolves.
func TestConcurrentListSpacesNeverCrossesAccounts(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()

	userA, err := database.CreateUser("Account A", "concurrency-a@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(A) error = %v", err)
	}
	userB, err := database.CreateUser("Account B", "concurrency-b@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(B) error = %v", err)
	}

	const iterationsPerUser = 40
	var wg sync.WaitGroup
	errs := make(chan error, iterationsPerUser*2)

	runFor := func(userID, otherOwnerID string) {
		defer wg.Done()
		spaces, err := database.ListSpaces(ctx, userID)
		if err != nil {
			errs <- err
			return
		}
		for _, space := range spaces {
			if space.OwnerUserID != userID {
				errs <- fmt.Errorf(
					"ListSpaces(%s) returned Space %s owned by %s (other account is %s)",
					userID, space.ID, space.OwnerUserID, otherOwnerID,
				)
				return
			}
		}
	}

	for i := 0; i < iterationsPerUser; i++ {
		wg.Add(2)
		go runFor(userA.ID, userB.ID)
		go runFor(userB.ID, userA.ID)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}
