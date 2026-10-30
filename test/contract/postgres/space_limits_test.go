package db

import (
	"context"
	"errors"
	"fmt"
	. "github.com/kannachi323/misty/server/internal/platform/db"
	"sync"
	"testing"
)

// Space counts the tests create. No account has a Space cap.
const (
	spaceCountSample      = 3
	largeSpaceCountSample = 10
)

func TestOwnedSpaceLimitDoesNotLimitJoining(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	member, err := database.CreateUser("basic-space-limit@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < spaceCountSample; index++ {
		if _, err := database.TestingCreateSpace(ctx, member.ID, fmt.Sprintf("Owned %d", index+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.TestingCreateSpace(ctx, member.ID, "Blocked ownership"); err != nil {
		t.Fatalf("create above owned limit error = %v, want ErrSpaceOwnershipLimit", err)
	}

	owner, err := database.CreateUser("max-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < spaceCountSample+2; index++ {
		space, err := database.TestingCreateSpace(ctx, owner.ID, fmt.Sprintf("Joined %d", index+1))
		if err != nil {
			t.Fatal(err)
		}
		invite, err := database.TestingInviteToSpace(ctx, owner.ID, space.ID, member.Email)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
			t.Fatalf("join %d at owned limit: %v", index+1, err)
		}
	}
	spaces, err := database.ListSpaces(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(standardSpaces(spaces)); got != spaceCountSample+1+(spaceCountSample+2) {
		t.Fatalf("standard memberships = %d", got)
	}
}

func TestOwnedSpacesHaveNoPlanCountCap(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("max-owned-limit@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < largeSpaceCountSample; index++ {
		if _, err := database.TestingCreateSpace(ctx, user.ID, fmt.Sprintf("Max Space %d", index+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.TestingCreateSpace(ctx, user.ID, "Eleventh"); err != nil {
		t.Fatalf("eleventh owned Space error = %v", err)
	}
}

func TestOwnershipTransferHasNoPlanCountCap(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	recipient, err := database.CreateUser("full-transfer-recipient@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < spaceCountSample; index++ {
		if _, err := database.TestingCreateSpace(ctx, recipient.ID, fmt.Sprintf("Recipient %d", index+1)); err != nil {
			t.Fatal(err)
		}
	}
	owner, err := database.CreateUser("limit-transfer-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	createTestSpace(t, database, ctx, owner.ID, "Home")
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Transfer candidate")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.TestingInviteToSpace(ctx, owner.ID, space.ID, recipient.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, recipient.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := database.TransferSpaceOwnership(ctx, owner.ID, space.ID, recipient.ID); err != nil {
		t.Fatalf("transfer above recipient limit error = %v", err)
	}
}

func TestOwnershipDowngradeDoesNotRestrictSpaces(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("space-downgrade@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < spaceCountSample+1; index++ {
		if _, err := database.TestingCreateSpace(ctx, user.ID, fmt.Sprintf("Existing %d", index+1)); err != nil {
			t.Fatal(err)
		}
	}
	spaces, err := database.ListSpaces(ctx, user.ID)
	if err != nil || len(standardSpaces(spaces)) != spaceCountSample+1 {
		t.Fatalf("Spaces after downgrade = %d, %v", len(standardSpaces(spaces)), err)
	}
	if _, err := database.TestingCreateSpace(ctx, user.ID, "Blocked after downgrade"); err != nil {
		t.Fatalf("create after downgrade error = %v", err)
	}
}

func TestConcurrentOwnedSpaceCreationHasNoCommercialCap(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("space-concurrent@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < spaceCountSample-1; index++ {
		if _, err := database.TestingCreateSpace(ctx, user.ID, fmt.Sprintf("Existing %d", index+1)); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for index := 0; index < 2; index++ {
		go func(index int) {
			ready.Done()
			<-start
			_, createErr := database.TestingCreateSpace(ctx, user.ID, fmt.Sprintf("Concurrent %d", index+1))
			errs <- createErr
		}(index)
	}
	ready.Wait()
	close(start)
	results := []error{<-errs, <-errs}
	successes, limited := 0, 0
	for _, operationErr := range results {
		switch {
		case operationErr == nil:
			successes++
		case errors.Is(operationErr, ErrSpaceOwnershipLimit):
			limited++
		default:
			t.Fatalf("concurrent create error = %v", operationErr)
		}
	}
	if successes != 2 || limited != 0 {
		t.Fatalf("concurrent results: successes=%d limited=%d", successes, limited)
	}
}
