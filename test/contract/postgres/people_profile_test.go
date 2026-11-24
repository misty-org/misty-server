package db

import (
	"context"
	"errors"
	"testing"

	. "github.com/misty-org/misty-server/internal/platform/db"
)

func joinTestSpace(t *testing.T, database *Database, ctx context.Context, owner, guest *User, space *Space) {
	t.Helper()
	invite, err := database.TestingInviteToSpace(ctx, owner.ID, space.ID, guest.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, guest.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
}

func requireHidden(t *testing.T, database *Database, ctx context.Context, viewer, other *User, why string) {
	t.Helper()
	if _, err := database.PersonBannerReference(ctx, viewer.ID, other.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("%s: banner error = %v, want ErrSpaceNotFound", why, err)
	}
	if _, err := database.MutualFriends(ctx, viewer.ID, other.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("%s: mutual friends error = %v, want ErrSpaceNotFound", why, err)
	}
	if _, err := database.SharedSpaces(ctx, viewer.ID, other.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("%s: shared Spaces error = %v, want ErrSpaceNotFound", why, err)
	}
}

func TestPersonProfileIsPublicToSignedInPeople(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	ana, err := database.CreateUserWithUsername("profile_ana", "profile-ana@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	ben, err := database.CreateUserWithUsername("profile_ben", "profile-ben@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.BumpUserBannerVersion(ben.ID, "banners/"+ben.ID+"-test"); err != nil {
		t.Fatal(err)
	}

	requireHidden(t, database, ctx, ana, ana, "asking about yourself")
	requireHidden(t, database, ctx, ana, &User{ID: "nobody"}, "an account that doesn't exist")

	banner, err := database.PersonBannerReference(ctx, ana.ID, ben.ID)
	if err != nil {
		t.Fatalf("a stranger's banner error = %v", err)
	}
	if banner.Version == 0 || banner.ObjectKey != "banners/"+ben.ID+"-test" {
		t.Fatalf("a stranger's banner = %#v, want the uploaded one", banner)
	}
	if mutual, err := database.MutualFriends(ctx, ana.ID, ben.ID); err != nil || len(mutual) != 0 {
		t.Fatalf("MutualFriends(strangers) = %#v, %v; want none", mutual, err)
	}
	if spaces, err := database.SharedSpaces(ctx, ana.ID, ben.ID); err != nil || len(spaces) != 0 {
		t.Fatalf("SharedSpaces(strangers) = %#v, %v; want none", spaces, err)
	}

	makeFriends(t, database, ctx, ana, ben)
	list, err := database.Friends(ctx, ana.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Friends) != 1 || list.Friends[0].BannerVersion != banner.Version {
		t.Fatalf("Friends() = %#v, want ben with banner_version %d", list.Friends, banner.Version)
	}
}

func TestPersonProfileListsMutualFriendsAndSharedSpacesOnly(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	ana, err := database.CreateUserWithUsername("mutual_ana", "mutual-ana@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	ben, err := database.CreateUserWithUsername("mutual_ben", "mutual-ben@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	cam, err := database.CreateUserWithUsername("mutual_cam", "mutual-cam@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	dee, err := database.CreateUserWithUsername("mutual_dee", "mutual-dee@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	makeFriends(t, database, ctx, ana, ben)
	makeFriends(t, database, ctx, ana, cam)
	makeFriends(t, database, ctx, ben, cam)
	makeFriends(t, database, ctx, ana, dee)

	both := createTestSpace(t, database, ctx, ana.ID, "Both of us")
	joinTestSpace(t, database, ctx, ana, ben, both)
	onlyAna := createTestSpace(t, database, ctx, ana.ID, "Ana alone")
	joinTestSpace(t, database, ctx, ana, cam, onlyAna)
	dm, err := database.OpenDirect(ctx, ana.ID, ben.ID)
	if err != nil {
		t.Fatal(err)
	}

	mutual, err := database.MutualFriends(ctx, ana.ID, ben.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mutual) != 1 || mutual[0].UserID != cam.ID {
		t.Fatalf("MutualFriends(ana, ben) = %#v, want only cam", mutual)
	}

	spaces, err := database.SharedSpaces(ctx, ana.ID, ben.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(spaces) != 1 || spaces[0].ID != both.ID || spaces[0].Name != "Both of us" {
		t.Fatalf("SharedSpaces(ana, ben) = %#v, want only %q", spaces, both.Name)
	}
	for _, space := range spaces {
		if space.ID == dm.ID || space.ID == personalSpaceOf(t, database, ctx, ana.ID).ID || space.ID == personalSpaceOf(t, database, ctx, ben.ID).ID {
			t.Fatalf("SharedSpaces lists a Personal or direct Space %s", space.ID)
		}
	}
}

func TestAnyoneSeesAPersonsPicture(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	ana, err := database.CreateUserWithUsername("picture_ana", "picture-ana@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	version, err := database.BumpUserAvatarVersion(ana.ID, "avatars/"+ana.ID+"-test")
	if err != nil {
		t.Fatal(err)
	}
	avatar, err := database.PersonAvatarReference(ctx, ana.ID)
	if err != nil {
		t.Fatalf("a stranger's picture error = %v", err)
	}
	if avatar.Version != version || avatar.ObjectKey != "avatars/"+ana.ID+"-test" {
		t.Fatalf("PersonAvatarReference() = %#v, want version %d", avatar, version)
	}
	if _, err := database.PersonAvatarReference(ctx, "nobody"); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("missing account's picture error = %v, want ErrSpaceNotFound", err)
	}
}
