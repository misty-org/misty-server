package db

import (
	"errors"
	"testing"

	. "github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/spaces"
)

// privateChannel gives the crew member a Crew role and makes a channel only
// the owner and the crew can read.
func (f rolesFixture) privateChannel(t *testing.T) *SpaceConversation {
	t.Helper()
	crewRole := f.role(t, "Crew")
	if _, err := f.database.SetSpaceMemberRoles(f.ctx, f.owner.ID, f.space.ID, f.crew.ID, []string{crewRole.ID}); err != nil {
		t.Fatal(err)
	}
	channel, err := f.database.CreateSpaceConversationWithOverwrites(f.ctx, f.owner.ID, f.space.ID, "Crew room", nil, []SpaceConversationOverwrite{
		{RoleID: f.everyone(t).ID, Deny: []string{spaces.PermissionMessagesRead}},
		{RoleID: crewRole.ID, Allow: []string{spaces.PermissionMessagesRead}},
	})
	if err != nil {
		t.Fatalf("CreateSpaceConversationWithOverwrites() error = %v", err)
	}
	return channel
}

func (f rolesFixture) lastEventID(t *testing.T, userID string) int64 {
	t.Helper()
	events, _, err := f.database.SpaceEventsAfter(f.ctx, userID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].ID
}

func TestPrivateChannelItemsAreHiddenFromMembersWithoutRead(t *testing.T) {
	f := newRolesFixture(t, "roles-items")
	channel := f.privateChannel(t)
	cursor := f.lastEventID(t, f.rest.ID)
	audience := SpaceResourceAudience{Kind: SpaceAudienceConversation, ConversationID: channel.ID}

	if _, err := f.database.CreateSpaceNoteWithVisibility(f.ctx, f.rest.ID, f.space.ID, "Sneaky", audience, "", "public"); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("a non-reader put a note in the private channel: %v", err)
	}
	note, err := f.database.CreateSpaceNoteWithVisibility(f.ctx, f.crew.ID, f.space.ID, "Crew plans", audience, "", "public")
	if err != nil {
		t.Fatalf("CreateSpaceNoteWithVisibility(crew) error = %v", err)
	}
	item := createTestImage(t, f.database, f.owner.ID, f.space.ID, "crew.jpg", "c")
	exec(t, f.database, `UPDATE space_library_items SET audience_kind='conversation',audience_conversation_id=$1,visibility='public' WHERE id=$2`, channel.ID, item.ID)

	notes, err := f.database.AccessibleSpaceNotes(f.ctx, f.rest.ID, f.space.ID)
	if err != nil || containsNote(notes, note.ID) {
		t.Fatalf("AccessibleSpaceNotes(rest) = %#v, %v, want no private-channel note", notes, err)
	}
	if crewNotes, err := f.database.AccessibleSpaceNotes(f.ctx, f.crew.ID, f.space.ID); err != nil || !containsNote(crewNotes, note.ID) {
		t.Fatalf("AccessibleSpaceNotes(crew) = %#v, %v, want the note", crewNotes, err)
	}
	if hits, err := f.database.SearchSpaceNotes(f.ctx, f.rest.ID, f.space.ID, "Crew plans", 20); err != nil || containsNote(hits, note.ID) {
		t.Fatalf("SearchSpaceNotes(rest) = %#v, %v, want no hit", hits, err)
	}
	if _, err := f.database.LibraryItem(f.ctx, f.rest.ID, f.space.ID, item.ID); err == nil {
		t.Fatal("a non-reader opened the private channel's Library item")
	}
	if _, err := f.database.LibraryItem(f.ctx, f.crew.ID, f.space.ID, item.ID); err != nil {
		t.Fatalf("LibraryItem(crew) error = %v", err)
	}
	items, err := f.database.LibraryItems(f.ctx, f.rest.ID, f.space.ID, LibraryItemQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range items {
		if listed.ID == item.ID {
			t.Fatal("a non-reader lists the private channel's Library item")
		}
	}
	events, _, err := f.database.SpaceEventsAfter(f.ctx, f.rest.ID, cursor, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.EntityID == note.ID {
			t.Fatalf("a non-reader got %s for the private channel's note", event.EventType)
		}
	}
}

func TestMentioningANonReaderInAPrivateChannelReachesNobodyElse(t *testing.T) {
	f := newRolesFixture(t, "roles-mention")
	channel := f.privateChannel(t)
	cursor := f.lastEventID(t, f.rest.ID)
	content := []MessageSpan{{Type: "text", Text: "ping "}, {Type: "mention", UserID: f.rest.ID, Label: "rest"}}
	message, _, err := f.database.TestingCreateSpaceConversationMessageWithReferences(f.ctx, f.crew.ID, f.space.ID, channel.ID, content, nil, nil, nil, "")
	if err != nil {
		t.Fatalf("posting a mention error = %v", err)
	}
	events, _, err := f.database.SpaceEventsAfter(f.ctx, f.rest.ID, cursor, 1000)
	if err != nil || containsSpaceEvent(events, "message.created", message.ID) {
		t.Fatalf("mentioned non-reader got events %#v, %v", events, err)
	}
	conversations, everyoneUnread, err := f.database.SpaceConversationsWithUnread(f.ctx, f.rest.ID, f.space.ID)
	if err != nil || len(conversations) != 0 || everyoneUnread != 0 {
		t.Fatalf("mentioned non-reader sees %#v unread %d, %v; want nothing", conversations, everyoneUnread, err)
	}
}

// The row security function answers as the API does.
func TestCanReadSpaceConversationFunctionMatchesTheAPI(t *testing.T) {
	f := newRolesFixture(t, "roles-rls")
	channel := f.privateChannel(t)
	dm, err := f.database.DirectMemberConversation(f.ctx, f.crew.ID, f.space.ID, f.rest.ID)
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := f.database.CreateUser("roles-rls-outsider@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	canRead := func(conversationID, userID string) bool {
		var readable bool
		if err := f.database.Conn.QueryRow(`SELECT misty_can_read_space_conversation($1,$2)`, conversationID, userID).Scan(&readable); err != nil {
			t.Fatal(err)
		}
		return readable
	}
	for _, check := range []struct {
		name, conversationID, userID string
		want                         bool
	}{
		{"owner, private channel", channel.ID, f.owner.ID, true},
		{"crew, private channel", channel.ID, f.crew.ID, true},
		{"rest, private channel", channel.ID, f.rest.ID, false},
		{"outsider, private channel", channel.ID, outsider.ID, false},
		{"participant, DM", dm.ID, f.rest.ID, true},
		{"owner outside the DM", dm.ID, f.owner.ID, false},
		{"missing conversation", "nope", f.owner.ID, false},
	} {
		if got := canRead(check.conversationID, check.userID); got != check.want {
			t.Errorf("%s = %v, want %v", check.name, got, check.want)
		}
	}
	// A deny on one of the member's roles loses to an allow on another.
	muted := f.role(t, "Muted")
	if _, err := f.database.SetSpaceMemberRoles(f.ctx, f.owner.ID, f.space.ID, f.crew.ID, append(roleIDsOf(t, f, f.crew.ID), muted.ID)); err != nil {
		t.Fatal(err)
	}
	overwrites := append(channel.Overwrites, SpaceConversationOverwrite{RoleID: muted.ID, Deny: []string{spaces.PermissionMessagesRead}})
	if _, err := f.database.SetSpaceConversationOverwrites(f.ctx, f.owner.ID, f.space.ID, channel.ID, overwrites); err != nil {
		t.Fatal(err)
	}
	if !canRead(channel.ID, f.crew.ID) {
		t.Fatal("the crew's allow lost to the muted role's deny")
	}
	// An administrator reads past every overwrite.
	admin := f.role(t, "Admin", spaces.PermissionAdministrator)
	if _, err := f.database.SetSpaceMemberRoles(f.ctx, f.owner.ID, f.space.ID, f.rest.ID, []string{admin.ID}); err != nil {
		t.Fatal(err)
	}
	if !canRead(channel.ID, f.rest.ID) {
		t.Fatal("an administrator was kept out of the private channel")
	}
	listed, err := f.database.SpaceConversations(f.ctx, f.rest.ID, f.space.ID)
	if err != nil || len(listed) != 2 {
		t.Fatalf("SpaceConversations(administrator) = %#v, %v, want the channel and the DM", listed, err)
	}
}

func roleIDsOf(t *testing.T, f rolesFixture, userID string) []string {
	t.Helper()
	members, err := f.database.SpaceMembers(f.ctx, f.owner.ID, f.space.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		if member.UserID == userID {
			return member.RoleIDs
		}
	}
	t.Fatalf("%s isn't a member", userID)
	return nil
}

func containsNote(notes []SpaceNote, noteID string) bool {
	for _, note := range notes {
		if note.ID == noteID {
			return true
		}
	}
	return false
}

func TestLibraryCountsLeaveOutItemsInChannelsTheMemberCantRead(t *testing.T) {
	f := newRolesFixture(t, "roles-facets")
	channel := f.privateChannel(t)
	shared := createTestImage(t, f.database, f.owner.ID, f.space.ID, "shared.jpg", "5")
	hidden := createTestImage(t, f.database, f.owner.ID, f.space.ID, "crew.jpg", "6")
	exec(t, f.database, `UPDATE space_library_items SET visibility='public',tags='["everyone"]'::jsonb WHERE id=$1`, shared.ID)
	exec(t, f.database, `UPDATE space_library_items SET audience_kind='conversation',audience_conversation_id=$1,visibility='public',tags='["crew"]'::jsonb WHERE id=$2`, channel.ID, hidden.ID)

	rest, err := f.database.LibraryFacets(f.ctx, f.rest.ID, f.space.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	crew, err := f.database.LibraryFacets(f.ctx, f.crew.ID, f.space.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if crew.Total != rest.Total+1 {
		t.Fatalf("totals: crew %d, rest %d; want the private channel's item counted only for the crew", crew.Total, rest.Total)
	}
	if facetHas(rest.Tags, "crew") || !facetHas(rest.Tags, "everyone") {
		t.Fatalf("rest tags = %#v, want everyone's tag and not the crew's", rest.Tags)
	}
	if !facetHas(crew.Tags, "crew") {
		t.Fatalf("crew tags = %#v, want the crew's tag", crew.Tags)
	}
}

func facetHas(facets []LibrarySearchFacet, value string) bool {
	for _, facet := range facets {
		if facet.Value == value {
			return true
		}
	}
	return false
}

func TestRoleMentionsNeedAMentionableRoleOrTheOwner(t *testing.T) {
	f := newRolesFixture(t, "roles-mentions")
	name, quiet, yes := "Moderator", "Staff", true
	mentionable, err := f.database.CreateSpaceRole(f.ctx, f.owner.ID, f.space.ID, SpaceRoleInput{Name: &name, Mentionable: &yes})
	if err != nil {
		t.Fatal(err)
	}
	unmentionable, err := f.database.CreateSpaceRole(f.ctx, f.owner.ID, f.space.ID, SpaceRoleInput{Name: &quiet})
	if err != nil {
		t.Fatal(err)
	}
	mention := func(role *SpaceRole) []MessageSpan {
		return []MessageSpan{{Type: "text", Text: "hey "}, {Type: "mention", RoleID: role.ID, Label: role.Name}}
	}
	posted, _, err := f.database.TestingCreateSpaceMessage(f.ctx, f.rest.ID, f.space.ID, mention(mentionable), nil)
	if err != nil || len(posted.Content) != 2 || posted.Content[1].RoleID != mentionable.ID {
		t.Fatalf("mentioning a mentionable role = %#v, %v, want the span kept", posted, err)
	}
	if _, _, err := f.database.TestingCreateSpaceMessage(f.ctx, f.rest.ID, f.space.ID, mention(unmentionable), nil); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("a member mentioning an unmentionable role error = %v, want ErrSpaceForbidden", err)
	}
	if _, _, err := f.database.TestingCreateSpaceMessage(f.ctx, f.owner.ID, f.space.ID, mention(unmentionable), nil); err != nil {
		t.Fatalf("the owner mentioning an unmentionable role error = %v", err)
	}
	everyone := f.everyone(t)
	if _, _, err := f.database.TestingCreateSpaceMessage(f.ctx, f.owner.ID, f.space.ID, mention(&everyone), nil); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("mentioning the everyone role error = %v, want ErrSpaceInvalid", err)
	}
	// Once the role is gone, the message keeps the span, and editing it still works.
	if err := f.database.DeleteSpaceRole(f.ctx, f.owner.ID, f.space.ID, mentionable.ID); err != nil {
		t.Fatal(err)
	}
	edited, err := f.database.UpdateSpaceMessage(f.ctx, f.rest.ID, f.space.ID, posted.ID, mention(mentionable), nil)
	if err != nil || edited.Content[1].RoleID != mentionable.ID {
		t.Fatalf("editing a message that mentions a deleted role = %#v, %v", edited, err)
	}
}
