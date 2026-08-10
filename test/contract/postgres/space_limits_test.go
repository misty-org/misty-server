package db

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSpaceMembershipLimitCountsOwnedAndJoinedButNotPendingInvitations(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	member, err := database.CreateUser("Basic Member", "basic-space-limit@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSpace(ctx, member.ID, "Owned one"); err != nil {
		t.Fatal(err)
	}
	paidOwner, err := database.CreateUser("Max Owner", "max-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetLicenseStateByID(paidOwner.LicenseID, TierMax, LicenseStatusActive, nil); err != nil {
		t.Fatal(err)
	}
	joinedSpace, err := database.CreateSpace(ctx, paidOwner.ID, "Joined third")
	if err != nil {
		t.Fatal(err)
	}
	joinInvite, err := database.InviteToSpace(ctx, paidOwner.ID, joinedSpace.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, joinInvite.ID, true); err != nil {
		t.Fatal(err)
	}

	nextSpace, err := database.CreateSpace(ctx, paidOwner.ID, "Pending fourth")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := database.InviteToSpace(ctx, paidOwner.ID, nextSpace.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, pending.ID, true); !errors.Is(err, ErrSpaceLimit) {
		t.Fatalf("accept at Basic limit error = %v, want ErrSpaceLimit", err)
	}
	if err := database.LeaveSpace(ctx, member.ID, joinedSpace.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, pending.ID, true); err != nil {
		t.Fatalf("accept after leaving a Space: %v", err)
	}
	spaces, err := database.ListSpaces(ctx, member.ID)
	spaces = standardSpaces(spaces)
	if err != nil || len(spaces) != BasicSpaceLimit {
		t.Fatalf("Basic memberships = %d, %v; want %d", len(spaces), err, BasicSpaceLimit)
	}
}

func TestSpaceMembershipDowngradePreservesExistingAndBlocksGrowth(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Downgrade Member", "space-downgrade@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetLicenseStateByID(user.LicenseID, TierMax, LicenseStatusActive, nil); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < BasicSpaceLimit; index++ {
		if _, err := database.CreateSpace(ctx, user.ID, fmt.Sprintf("Max Space %d", index+1)); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.SetLicenseStateByID(user.LicenseID, TierBasic, LicenseStatusActive, nil); err != nil {
		t.Fatal(err)
	}
	spaces, err := database.ListSpaces(ctx, user.ID)
	spaces = standardSpaces(spaces)
	if err != nil || len(spaces) != BasicSpaceLimit+1 {
		t.Fatalf("memberships after downgrade = %d, %v", len(spaces), err)
	}
	if _, err := database.CreateSpace(ctx, user.ID, "Blocked after downgrade"); !errors.Is(err, ErrSpaceLimit) {
		t.Fatalf("create after downgrade error = %v, want ErrSpaceLimit", err)
	}
}

func TestConcurrentSpaceMembershipOperationsCannotExceedLimit(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Concurrent Member", "space-concurrent@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < BasicSpaceLimit-2; index++ {
		if _, err := database.CreateSpace(ctx, user.ID, fmt.Sprintf("Existing %d", index+1)); err != nil {
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
			_, createErr := database.CreateSpace(ctx, user.ID, fmt.Sprintf("Concurrent %d", index+1))
			errs <- createErr
		}(index)
	}
	ready.Wait()
	close(start)
	first, second := <-errs, <-errs
	successes, limited := 0, 0
	for _, operationErr := range []error{first, second} {
		switch {
		case operationErr == nil:
			successes++
		case errors.Is(operationErr, ErrSpaceLimit):
			limited++
		default:
			t.Fatalf("concurrent create error = %v", operationErr)
		}
	}
	if successes != 1 || limited != 1 {
		t.Fatalf("concurrent results: successes=%d limited=%d", successes, limited)
	}
	spaces, err := database.ListSpaces(ctx, user.ID)
	spaces = standardSpaces(spaces)
	if err != nil || len(spaces) != BasicSpaceLimit {
		t.Fatalf("memberships after concurrent creates = %d, %v", len(spaces), err)
	}
}
