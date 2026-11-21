package db

import (
	"testing"

	. "github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/spaces"
)

// A voice channel's call is open to whoever may read the channel, and to nobody else: a hidden voice
// channel is closed to those it hides from, a text channel has no call, and a channel is joined only
// under its own Space.
func TestVoiceChannelCallsFollowWhoMayReadTheChannel(t *testing.T) {
	f := newRolesFixture(t, "voice-calls")
	crewRole := f.role(t, "Crew")
	if _, err := f.database.SetSpaceMemberRoles(f.ctx, f.owner.ID, f.space.ID, f.crew.ID, []string{crewRole.ID}); err != nil {
		t.Fatal(err)
	}
	outsider, err := f.database.CreateUser("voice-calls-outsider@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	open, err := f.database.CreateSpaceVoiceChannel(f.ctx, f.owner.ID, f.space.ID, "General", nil)
	if err != nil {
		t.Fatalf("CreateSpaceVoiceChannel(open) error = %v", err)
	}
	everyone := f.everyone(t)
	hidden, err := f.database.CreateSpaceVoiceChannel(f.ctx, f.owner.ID, f.space.ID, "Crew call", []SpaceConversationOverwrite{
		{RoleID: everyone.ID, Deny: []string{spaces.PermissionMessagesRead}},
		{RoleID: crewRole.ID, Allow: []string{spaces.PermissionMessagesRead}},
	})
	if err != nil {
		t.Fatalf("CreateSpaceVoiceChannel(hidden) error = %v", err)
	}
	text, err := f.database.CreateSpaceConversation(f.ctx, f.owner.ID, f.space.ID, "Chat", nil)
	if err != nil {
		t.Fatalf("CreateSpaceConversation(text) error = %v", err)
	}
	room := func(spaceID, channelID string) string { return "space:" + spaceID + ":voice:" + channelID }

	cases := []struct {
		name   string
		userID string
		room   string
		want   bool
	}{
		{"a member joins an open voice channel", f.rest.ID, room(f.space.ID, open.ID), true},
		{"the crew joins its hidden voice channel", f.crew.ID, room(f.space.ID, hidden.ID), true},
		{"the owner joins any of them", f.owner.ID, room(f.space.ID, hidden.ID), true},
		{"a member it hides from can't", f.rest.ID, room(f.space.ID, hidden.ID), false},
		{"someone outside the Space can't", outsider.ID, room(f.space.ID, open.ID), false},
		{"a text channel has no call", f.rest.ID, room(f.space.ID, text.ID), false},
		{"a channel under another Space's id isn't one", f.rest.ID, room("spc_elsewhere", open.ID), false},
		{"a channel that doesn't exist isn't one", f.rest.ID, room(f.space.ID, "cnv_missing"), false},
		{"a room name of no known kind is closed", f.rest.ID, "space:" + f.space.ID, false},
	}
	for _, c := range cases {
		got, err := f.database.CanJoinCall(f.ctx, c.userID, c.room)
		if err != nil {
			t.Fatalf("%s: CanJoinCall error = %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: CanJoinCall = %v, want %v", c.name, got, c.want)
		}
	}
}
