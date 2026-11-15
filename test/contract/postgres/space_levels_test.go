package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/test/testkit"
)

// Misty Cloud's levels, small enough to test: Level 1 at 2 Boosts, Level 2 at 4.
const testSpaceLevels = `{"thresholds":[2,4],"storage_bytes_per_boost":100,"levels":[
	{"members":2,"upload_bytes":1000,"agents":1},
	{"members":3,"storage_bytes":5000,"upload_bytes":2000,"agents":2,"custom_icon":true},
	{"members":null,"storage_bytes":10000,"upload_bytes":null,"agents":null,"custom_icon":true,"invite_link":true}]}`

func TestSpaceLevelsRiseWithMembersBoosts(t *testing.T) {
	t.Setenv("MISTY_SPACE_LEVELS", testSpaceLevels)
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	owner, _, err := database.CreateOperatorAccount(ctx, "level.owner@example.com", AccountLimits{})
	if err != nil {
		t.Fatal(err)
	}
	friend, _, err := database.CreateOperatorAccount(ctx, "level.friend@example.com", AccountLimits{})
	if err != nil {
		t.Fatal(err)
	}
	outsider, _, err := database.CreateOperatorAccount(ctx, "level.outsider@example.com", AccountLimits{})
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Boosted")
	if err != nil {
		t.Fatal(err)
	}
	level := func() *SpaceLevel {
		t.Helper()
		state, err := database.SpaceBoosts(ctx, owner.ID, space.ID)
		if err != nil || !state.Enabled {
			t.Fatalf("boosts = %+v %v", state, err)
		}
		return state.Level
	}
	if got := level(); got.Level != 0 || got.Boosts != 0 || got.NextLevel != 2 || *got.Limits.Members != 2 {
		t.Fatalf("base = %+v", got)
	}

	// Base: 2 people, invitations included. The owner and one invitation fill it.
	invitation, err := database.TestingInviteToSpace(ctx, owner.ID, space.ID, friend.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.TestingInviteToSpace(ctx, owner.ID, space.ID, "third@example.com"); !errors.Is(err, ErrSpacePeopleLimit) {
		t.Fatalf("third person at base = %v", err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, friend.ID, invitation.ID, true); err != nil {
		t.Fatal(err)
	}

	// An outsider's Boosts don't count; members' do, together.
	if err := database.SetSpaceBoosts(ctx, space.ID, outsider.ID, 5); err != nil {
		t.Fatal(err)
	}
	if got := level(); got.Level != 0 {
		t.Fatalf("outsider's boosts counted: %+v", got)
	}
	if err := database.SetSpaceBoosts(ctx, space.ID, owner.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := database.SetSpaceBoosts(ctx, space.ID, friend.ID, 2); err != nil {
		t.Fatal(err)
	}
	got := level()
	if got.Level != 1 || got.Boosts != 3 || got.NextLevel != 4 || !got.Limits.CustomIcon {
		t.Fatalf("level 1 = %+v", got)
	}
	// Each Boost past the threshold adds storage: 5000 + 1×100.
	if *got.Limits.StorageBytes != 5100 {
		t.Fatalf("level 1 storage = %d", *got.Limits.StorageBytes)
	}
	if _, err := database.TestingInviteToSpace(ctx, owner.ID, space.ID, "third@example.com"); err != nil {
		t.Fatalf("third person at level 1 = %v", err)
	}
	state, err := database.SpaceBoosts(ctx, friend.ID, space.ID)
	if err != nil || len(state.Boosters) != 2 || state.Boosters[0].UserID != friend.ID {
		t.Fatalf("boosters = %+v %v", state, err)
	}
	if _, err := database.SpaceBoosts(ctx, outsider.ID, space.ID); err == nil {
		t.Fatal("an outsider read the Space's boosts")
	}

	// Agents: level 1 allows 2. Renaming one already in the Space never counts twice.
	for _, agent := range []string{"agent-a", "agent-b"} {
		if _, err := database.AddSpaceAgent(ctx, owner.ID, space.ID, agent, agent); err != nil {
			t.Fatalf("%s = %v", agent, err)
		}
	}
	if _, err := database.AddSpaceAgent(ctx, owner.ID, space.ID, "agent-a", "renamed"); err != nil {
		t.Fatalf("rename = %v", err)
	}
	if _, err := database.AddSpaceAgent(ctx, friend.ID, space.ID, "agent-c", "c"); !errors.Is(err, ErrSpaceAgentLimit) {
		t.Fatalf("third agent = %v", err)
	}

	// Uploads: level 1 allows 2000 bytes a file, and the Space's own 5100 bytes of storage.
	upload := func(size int64) error {
		key := strings.Repeat("a", 64)
		_, err := database.CreateLibraryUpload(ctx, friend.ID, space.ID, "library", "f.bin", "application/octet-stream", size,
			key, "objects/"+time.Now().Format("150405.000000000"), "token-"+time.Now().Format("150405.000000000"), time.Now().Add(time.Hour))
		return err
	}
	if err := upload(2001); !errors.Is(err, ErrSpaceUploadLimit) {
		t.Fatalf("too large = %v", err)
	}
	if err := database.SetAccountLimits(ctx, friend.ID, AccountLimits{UploadBytes: limit(3000)}); err != nil {
		t.Fatal(err)
	}
	if err := upload(2500); err != nil {
		t.Fatalf("a larger file the uploader's own plan allows = %v", err)
	}
	if err := upload(2000); err != nil {
		t.Fatalf("second file = %v", err)
	}
	if err := upload(1000); !errors.Is(err, ErrSpaceStorageLimit) {
		t.Fatalf("past the Space's storage = %v", err)
	}
	// The boosted Space's files are the Space's, not the uploader's: a tiny personal limit
	// doesn't stop them, and they don't count against it elsewhere.
	if err := database.SetAccountLimits(ctx, friend.ID, AccountLimits{StorageBytes: limit(10), UploadBytes: limit(3000)}); err != nil {
		t.Fatal(err)
	}
	usage, err := database.AccountLimitsFor(ctx, friend.ID)
	if err != nil || *usage.StorageBytes != 10 {
		t.Fatal(err)
	}

	// Boosts lapse: back to the base level, and nothing already in the Space goes away.
	if err := database.SetSpaceBoosts(ctx, space.ID, friend.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got := level(); got.Level != 0 || got.Boosts != 1 {
		t.Fatalf("after lapse = %+v", got)
	}
	members, err := database.SpaceMembers(ctx, owner.ID, space.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("members after lapse = %d %v", len(members), err)
	}
	if _, err := database.AddSpaceAgent(ctx, owner.ID, space.ID, "agent-d", "d"); !errors.Is(err, ErrSpaceAgentLimit) {
		t.Fatalf("agent after lapse = %v", err)
	}
	if err := database.SetSpaceBoosts(ctx, "space_missing", owner.ID, 1); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("missing Space = %v", err)
	}
}

func TestWithoutSpaceLevelsEverySpaceIsUnlimited(t *testing.T) {
	t.Setenv("MISTY_SPACE_LEVELS", "")
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	owner, _, err := database.CreateOperatorAccount(ctx, "selfhost@example.com", AccountLimits{})
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Self-hosted")
	if err != nil {
		t.Fatal(err)
	}
	state, err := database.SpaceBoosts(ctx, owner.ID, space.ID)
	if err != nil || state.Enabled || state.Level != nil {
		t.Fatalf("self-hosted = %+v %v", state, err)
	}
	for i := 0; i < 5; i++ {
		if _, err := database.AddSpaceAgent(ctx, owner.ID, space.ID, "agent-"+string(rune('a'+i)), "agent"); err != nil {
			t.Fatalf("agent %d = %v", i, err)
		}
	}
}
