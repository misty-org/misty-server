package db

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func joinQuotaTestSpace(t *testing.T, database *Database, ctx context.Context, owner User, member User, name string) Space {
	t.Helper()
	space, err := database.CreateSpace(ctx, owner.ID, name)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	return *space
}

func TestPersonalStorageUsageAggregatesAcrossJoinedSpaces(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	member, err := database.CreateUser("Basic Contributor", "global-storage-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	firstOwner, _ := database.CreateUser("First Max Owner", "global-storage-owner-1@example.com", "password123")
	secondOwner, _ := database.CreateUser("Second Max Owner", "global-storage-owner-2@example.com", "password123")
	for _, owner := range []User{*firstOwner, *secondOwner} {
		if err := database.SetLicenseStateByID(owner.LicenseID, TierMax, LicenseStatusActive, nil); err != nil {
			t.Fatal(err)
		}
	}
	first := joinQuotaTestSpace(t, database, ctx, *firstOwner, *member, "First joined capacity")
	second := joinQuotaTestSpace(t, database, ctx, *secondOwner, *member, "Second joined capacity")
	firstReservations := reserveQuotaBytes(t, database, ctx, member.ID, first.ID, "global-first", BasicStorageBytes/2)
	secondReservations := reserveQuotaBytes(t, database, ctx, member.ID, second.ID, "global-second", BasicStorageBytes/2)

	_, err = database.CreateLibraryUpload(ctx, member.ID, second.ID, UploadPurposeLibrary,
		"overflow.bin", "application/octet-stream", 1, strings.Repeat("e", 64),
		"library/global-overflow", "global-overflow-token", time.Now().Add(time.Hour))
	if !errors.Is(err, ErrPersonalStorageQuota) {
		t.Fatalf("global personal overflow = %v, want ErrPersonalStorageQuota", err)
	}
	usage, err := database.OwnerStorageUsage(ctx, member.ID)
	if err != nil || usage.ReservedBytes != BasicStorageBytes || len(usage.Spaces) != 2 || !usage.Personal.OverQuota && usage.RemainingBytes != 0 {
		t.Fatalf("personal usage = %#v, %v", usage, err)
	}
	releaseQuota(t, database, ctx, firstReservations)
	releaseQuota(t, database, ctx, secondReservations)
}

func TestSpaceStorageCapacityUsesOwnerPlanNotMemberPlan(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Basic Space Owner", "space-capacity-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Max Contributor", "space-capacity-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetLicenseStateByID(member.LicenseID, TierMax, LicenseStatusActive, nil); err != nil {
		t.Fatal(err)
	}
	space := joinQuotaTestSpace(t, database, ctx, *owner, *member, "Basic owner capacity")
	reservations := reserveQuotaBytes(t, database, ctx, member.ID, space.ID, "space-owner-cap", BasicStorageBytes)
	_, err = database.CreateLibraryUpload(ctx, member.ID, space.ID, UploadPurposeLibrary,
		"overflow.bin", "application/octet-stream", 1, strings.Repeat("f", 64),
		"library/space-overflow", "space-overflow-token", time.Now().Add(time.Hour))
	if !errors.Is(err, ErrSpaceStorageQuota) {
		t.Fatalf("owner-plan Space overflow = %v, want ErrSpaceStorageQuota", err)
	}
	usage, err := database.SpaceStorageUsage(ctx, member.ID, space.ID)
	if err != nil || usage.SpaceLimitBytes != BasicStorageBytes || usage.SpaceRemainingBytes != 0 || usage.PersonalLimitBytes != MaxStorageBytes {
		t.Fatalf("cross-plan storage usage = %#v, %v", usage, err)
	}
	releaseQuota(t, database, ctx, reservations)
}

func TestConcurrentReservationsAcrossSpacesCannotRacePersonalLimit(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	member, err := database.CreateUser("Concurrent Contributor", "concurrent-storage-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	firstOwner, _ := database.CreateUser("Concurrent Owner One", "concurrent-storage-owner-1@example.com", "password123")
	secondOwner, _ := database.CreateUser("Concurrent Owner Two", "concurrent-storage-owner-2@example.com", "password123")
	for _, owner := range []*User{firstOwner, secondOwner} {
		if err := database.SetLicenseStateByID(owner.LicenseID, TierMax, LicenseStatusActive, nil); err != nil {
			t.Fatal(err)
		}
	}
	first := joinQuotaTestSpace(t, database, ctx, *firstOwner, *member, "Concurrent first")
	second := joinQuotaTestSpace(t, database, ctx, *secondOwner, *member, "Concurrent second")
	baseline := reserveQuotaBytes(t, database, ctx, member.ID, first.ID, "concurrent-baseline", BasicStorageBytes-1)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for index, spaceID := range []string{first.ID, second.ID} {
		go func(index int, spaceID string) {
			ready.Done()
			<-start
			_, reserveErr := database.CreateLibraryUpload(ctx, member.ID, spaceID, UploadPurposeLibrary,
				"race.bin", "application/octet-stream", 1, strings.Repeat("a", 64),
				"library/concurrent-race-"+spaceID, "concurrent-race-token-"+spaceID,
				time.Now().Add(time.Hour))
			errs <- reserveErr
		}(index, spaceID)
	}
	ready.Wait()
	close(start)
	results := []error{<-errs, <-errs}
	successes, limited := 0, 0
	for _, result := range results {
		if result == nil {
			successes++
		} else if errors.Is(result, ErrPersonalStorageQuota) {
			limited++
		} else {
			t.Fatalf("concurrent reservation error = %v", result)
		}
	}
	if successes != 1 || limited != 1 {
		t.Fatalf("concurrent storage results: successes=%d limited=%d", successes, limited)
	}
	usage, err := database.OwnerStorageUsage(ctx, member.ID)
	if err != nil || usage.ReservedBytes != BasicStorageBytes {
		t.Fatalf("concurrent personal usage = %#v, %v", usage, err)
	}
	releaseQuota(t, database, ctx, baseline)
}
