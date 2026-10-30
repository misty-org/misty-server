package db

import (
	"context"
	"testing"

)

func TestOwnedSpacesStorageUsageAddsUp(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("pooled-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	firstSpace := createTestSpace(t, database, ctx, owner.ID, "First pool consumer")
	project, err := database.TestingCreateSpace(ctx, owner.ID, "Second pool consumer")
	if err != nil {
		t.Fatal(err)
	}
	const half = int64(1_000_000)
	first := reserveQuotaBytes(t, database, ctx, owner.ID, firstSpace.ID, "pool-first", half)
	second := reserveQuotaBytes(t, database, ctx, owner.ID, project.ID, "pool-second", half)
	ownerUsage, err := database.OwnerStorageUsage(ctx, owner.ID)
	if err != nil || ownerUsage.ReservedBytes != 2*half || len(ownerUsage.Spaces) != 2 {
		t.Fatalf("owner usage = %#v, %v", ownerUsage, err)
	}
	releaseQuota(t, database, ctx, first)
	releaseQuota(t, database, ctx, second)
}
