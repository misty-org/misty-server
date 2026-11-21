package db

import (
	"context"
	"errors"
	"testing"

	. "github.com/misty-org/misty-server/internal/platform/db"
)

func personalSpaceOf(t *testing.T, database *Database, ctx context.Context, userID string) Space {
	t.Helper()
	spaces, err := database.ListSpaces(ctx, userID)
	if err != nil {
		t.Fatalf("ListSpaces() error = %v", err)
	}
	for _, space := range spaces {
		if space.IsDefault {
			return space
		}
	}
	t.Fatalf("no Personal among %#v", spaces)
	return Space{}
}

func makeFriends(t *testing.T, database *Database, ctx context.Context, a, b *User) {
	t.Helper()
	if err := database.SendFriendRequest(ctx, a.ID, b.Username); err != nil {
		t.Fatalf("SendFriendRequest() error = %v", err)
	}
	if err := database.AcceptFriendRequest(ctx, b.ID, a.ID); err != nil {
		t.Fatalf("AcceptFriendRequest() error = %v", err)
	}
}

func TestPersonalSpaceCannotBeShared(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUserWithUsername("personal_owner", "personal-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	guest, err := database.CreateUserWithUsername("personal_guest", "personal-guest@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	personal := personalSpaceOf(t, database, ctx, owner.ID)
	if _, err := database.TestingInviteToSpace(ctx, owner.ID, personal.ID, guest.Email); !errors.Is(err, ErrSpaceNotShareable) {
		t.Fatalf("invite into Personal error = %v, want ErrSpaceNotShareable", err)
	}
}

func TestFriendRequestsRevealNothingAndHoldTheLimit(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	sender, err := database.CreateUserWithUsername("friend_sender", "friend-sender@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	private, err := database.CreateUserWithUsername("friend_private", "friend-private@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := database.CreateUserWithUsername("friend_blocker", "friend-blocker@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetPrivacy(ctx, private.ID, FriendPrivacy{FriendRequests: "shared_spaces"}); err != nil {
		t.Fatal(err)
	}
	if err := database.Block(ctx, blocker.ID, sender.ID); err != nil {
		t.Fatal(err)
	}
	// Nobody by that name, someone who takes requests only from shared Spaces, and someone who
	// blocked the sender all answer like a sent request, and none of them gets one.
	for _, username := range []string{"nobody_by_this_name", private.Username, blocker.Username} {
		if err := database.SendFriendRequest(ctx, sender.ID, username); err != nil {
			t.Fatalf("SendFriendRequest(%s) error = %v, want nil", username, err)
		}
	}
	for _, user := range []*User{private, blocker} {
		list, err := database.Friends(ctx, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(list.Incoming) != 0 {
			t.Fatalf("%s got requests %#v, want none", user.Username, list.Incoming)
		}
	}
	// Three sent today; a limit of three refuses the fourth.
	three := int64(3)
	if err := database.SetAccountLimits(ctx, sender.ID, AccountLimits{FriendRequestsPerDay: &three}); err != nil {
		t.Fatal(err)
	}
	if err := database.SendFriendRequest(ctx, sender.ID, "someone_else"); !errors.Is(err, ErrFriendRequestLimit) {
		t.Fatalf("fourth request in a day error = %v, want ErrFriendRequestLimit", err)
	}
}

func TestDirectMessagesNeedFriendsAndEndWithTheFriendship(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	ana, err := database.CreateUserWithUsername("dm_ana", "dm-ana@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	ben, err := database.CreateUserWithUsername("dm_ben", "dm-ben@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	// Sharing a Space never opens a DM.
	shared := createTestSpace(t, database, ctx, ana.ID, "Shared work")
	invite, err := database.TestingInviteToSpace(ctx, ana.ID, shared.ID, ben.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, ben.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := database.OpenDirect(ctx, ana.ID, ben.ID); !errors.Is(err, ErrNotFriends) {
		t.Fatalf("OpenDirect(not friends) error = %v, want ErrNotFriends", err)
	}
	makeFriends(t, database, ctx, ana, ben)
	dm, err := database.OpenDirect(ctx, ana.ID, ben.ID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := database.OpenDirect(ctx, ben.ID, ana.ID)
	if err != nil || again.ID != dm.ID {
		t.Fatalf("OpenDirect(other way) = %#v, %v; want the same DM %s", again, err, dm.ID)
	}
	if _, _, err := database.CreateSpaceMessageWithReferencesAndClientNonce(ctx, ben.ID, dm.ID, []MessageSpan{{Type: "text", Text: "hey"}}, nil, nil, "", ""); err != nil {
		t.Fatalf("message in the DM error = %v", err)
	}
	// A DM is never one of the person's Spaces.
	spaces, err := database.ListSpaces(ctx, ana.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, space := range spaces {
		if space.ID == dm.ID {
			t.Fatalf("ListSpaces lists the DM %s", dm.ID)
		}
	}
	listed, err := database.Directs(ctx, ana.ID)
	if err != nil || len(listed) != 1 || listed[0].UnreadCount != 1 || listed[0].Kind != "direct" {
		t.Fatalf("Directs() = %#v, %v; want one direct DM with one unread", listed, err)
	}
	if _, err := database.TestingInviteToSpace(ctx, ana.ID, dm.ID, "someone@example.com"); !errors.Is(err, ErrSpaceNotShareable) {
		t.Fatalf("invite into a DM error = %v, want ErrSpaceNotShareable", err)
	}
	// Unfriending deletes the one-to-one DM for both.
	if err := database.Unfriend(ctx, ben.ID, ana.ID); err != nil {
		t.Fatal(err)
	}
	for _, user := range []*User{ana, ben} {
		listed, err := database.Directs(ctx, user.ID)
		if err != nil || len(listed) != 0 {
			t.Fatalf("Directs(%s) after unfriending = %#v, %v; want none", user.Username, listed, err)
		}
	}
}

func TestStatusAudienceIsFriendsAndSharedSpacesLessBlocks(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	me, err := database.CreateUserWithUsername("status_me", "status-me@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	friend, err := database.CreateUserWithUsername("status_friend", "status-friend@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := database.CreateUserWithUsername("status_stranger", "status-stranger@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	makeFriends(t, database, ctx, me, friend)
	audience, err := database.StatusAudience(ctx, me.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(audience) != 1 || audience[0] != friend.ID {
		t.Fatalf("StatusAudience = %v, want only %s", audience, friend.ID)
	}
	if err := database.Block(ctx, me.ID, friend.ID); err != nil {
		t.Fatal(err)
	}
	audience, err = database.StatusAudience(ctx, me.ID)
	if err != nil || len(audience) != 0 {
		t.Fatalf("StatusAudience after blocking = %v, %v; want nobody", audience, err)
	}
	_ = stranger
	saved, err := database.SetChosenStatus(ctx, me.ID, ChosenStatus{Chosen: "dnd", CustomText: "  In meetings  "})
	if err != nil || saved.Chosen != "dnd" || saved.CustomText != "In meetings" {
		t.Fatalf("SetChosenStatus = %#v, %v", saved, err)
	}
}

func TestSpaceDirectMessagesMoveWithTheirMessages(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	ana, err := database.CreateUserWithUsername("move_ana", "move-ana@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	ben, err := database.CreateUserWithUsername("move_ben", "move-ben@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, ana.ID, "Old DMs")
	invite, err := database.TestingInviteToSpace(ctx, ana.ID, space.ID, ben.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, ben.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	old, err := database.DirectMemberConversation(ctx, ana.ID, space.ID, ben.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first", "second"} {
		if _, _, err := database.CreateSpaceConversationMessageWithReferencesAndClientNonce(ctx, ana.ID, space.ID, old.ID, []MessageSpan{{Type: "text", Text: text}}, nil, nil, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.MigrateSpaceDirectMessages(ctx); err != nil {
		t.Fatalf("MigrateSpaceDirectMessages() error = %v", err)
	}
	listed, err := database.Directs(ctx, ben.ID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("Directs() after the move = %#v, %v; want one", listed, err)
	}
	// They aren't friends, so the DM keeps its history read-only.
	if !listed[0].ReadOnly {
		t.Fatalf("moved DM between non-friends ReadOnly = false, want true")
	}
	messages, err := database.SpaceMessages(ctx, ben.ID, listed[0].ID, 0, 20)
	if err != nil || len(messages) != 2 {
		t.Fatalf("moved messages = %#v, %v; want both", messages, err)
	}
	if _, _, err := database.CreateSpaceMessageWithReferencesAndClientNonce(ctx, ben.ID, listed[0].ID, []MessageSpan{{Type: "text", Text: "hi"}}, nil, nil, "", ""); !errors.Is(err, ErrNotFriends) {
		t.Fatalf("message in a read-only DM error = %v, want ErrNotFriends", err)
	}
	// Running it again moves nothing twice.
	if err := database.MigrateSpaceDirectMessages(ctx); err != nil {
		t.Fatal(err)
	}
}
