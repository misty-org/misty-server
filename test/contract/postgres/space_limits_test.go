package db

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestOwnedSpaceLimitDoesNotLimitJoining(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	member, err := database.CreateUser("Basic Member", "basic-space-limit@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < BasicSpaceLimit; index++ {
		if _, err := database.CreateSpace(ctx, member.ID, fmt.Sprintf("Owned %d", index+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.CreateSpace(ctx, member.ID, "Blocked ownership"); !errors.Is(err, ErrSpaceOwnershipLimit) {
		t.Fatalf("create above owned limit error = %v, want ErrSpaceOwnershipLimit", err)
	}

	owner, err := database.CreateUser("Max Owner", "max-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetLicenseStateByID(owner.LicenseID, TierMax, LicenseStatusActive, nil); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < BasicSpaceLimit+2; index++ {
		space, err := database.CreateSpace(ctx, owner.ID, fmt.Sprintf("Joined %d", index+1))
		if err != nil {
			t.Fatal(err)
		}
		invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
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
	if got := len(standardSpaces(spaces)); got != BasicSpaceLimit+(BasicSpaceLimit+2) {
		t.Fatalf("standard memberships = %d", got)
	}
}

func TestMaxPlanOwnedSpacesAreCappedAtTen(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Max Owner", "max-owned-limit@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetLicenseStateByID(user.LicenseID, TierMax, LicenseStatusActive, nil); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxSpaceLimit; index++ {
		if _, err := database.CreateSpace(ctx, user.ID, fmt.Sprintf("Max Space %d", index+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.CreateSpace(ctx, user.ID, "Eleventh"); !errors.Is(err, ErrSpaceOwnershipLimit) {
		t.Fatalf("eleventh owned Space error = %v", err)
	}
}

func TestOwnershipTransferEnforcesRecipientOwnedSpaceLimit(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	recipient, err := database.CreateUser("Full Recipient", "full-transfer-recipient@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < BasicSpaceLimit; index++ {
		if _, err := database.CreateSpace(ctx, recipient.ID, fmt.Sprintf("Recipient %d", index+1)); err != nil {
			t.Fatal(err)
		}
	}
	owner, err := database.CreateUser("Transfer Owner", "limit-transfer-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	createTestSpace(t, database, ctx, owner.ID, "Home")
	space, err := database.CreateSpace(ctx, owner.ID, "Transfer candidate")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, recipient.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, recipient.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := database.TransferSpaceOwnership(ctx, owner.ID, space.ID, recipient.ID); !errors.Is(err, ErrSpaceOwnershipLimit) {
		t.Fatalf("transfer above recipient limit error = %v", err)
	}
}

func TestOwnershipDowngradePreservesExistingAndBlocksGrowth(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Downgrade Owner", "space-downgrade@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetLicenseStateByID(user.LicenseID, TierMax, LicenseStatusActive, nil); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < BasicSpaceLimit+1; index++ {
		if _, err := database.CreateSpace(ctx, user.ID, fmt.Sprintf("Existing %d", index+1)); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.SetLicenseStateByID(user.LicenseID, TierBasic, LicenseStatusActive, nil); err != nil {
		t.Fatal(err)
	}
	spaces, err := database.ListSpaces(ctx, user.ID)
	if err != nil || len(standardSpaces(spaces)) != BasicSpaceLimit+1 {
		t.Fatalf("Spaces after downgrade = %d, %v", len(standardSpaces(spaces)), err)
	}
	if _, err := database.CreateSpace(ctx, user.ID, "Blocked after downgrade"); !errors.Is(err, ErrSpaceOwnershipLimit) {
		t.Fatalf("create after downgrade error = %v", err)
	}
}

func TestConcurrentOwnedSpaceCreationCannotExceedLimit(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Concurrent Owner", "space-concurrent@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < BasicSpaceLimit-1; index++ {
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
	if successes != 1 || limited != 1 {
		t.Fatalf("concurrent results: successes=%d limited=%d", successes, limited)
	}
}
