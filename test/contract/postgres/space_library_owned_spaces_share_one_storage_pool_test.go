package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestOwnedSpacesShareOneStoragePool(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Pooled Owner", "pooled-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	firstSpace := createTestSpace(t, database, ctx, owner.ID, "First pool consumer")
	project, err := database.CreateSpace(ctx, owner.ID, "Second pool consumer")
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("c", 64)
	// Half the pool is consumed from each Space, so overflow can only be
	// explained by the two Spaces sharing one owner-level pool.
	first := reserveQuotaBytes(t, database, ctx, owner.ID, firstSpace.ID, "pool-first", FreeStorageBytes/2)
	second := reserveQuotaBytes(t, database, ctx, owner.ID, project.ID, "pool-second", FreeStorageBytes/2)
	if _, err := database.CreateLibraryUpload(ctx, owner.ID, project.ID, "library", "overflow.bin", "application/octet-stream", 1, digest, "library/pool-overflow", "pool-overflow-token", time.Now().Add(time.Hour)); !errors.Is(err, ErrLibraryQuota) {
		t.Fatalf("cross-Space overflow = %v, want ErrLibraryQuota", err)
	}
	ownerUsage, err := database.OwnerStorageUsage(ctx, owner.ID)
	if err != nil || ownerUsage.ReservedBytes != FreeStorageBytes || len(ownerUsage.Spaces) != 2 {
		t.Fatalf("owner pool = %#v, %v", ownerUsage, err)
	}
	releaseQuota(t, database, ctx, first)
	releaseQuota(t, database, ctx, second)
}
