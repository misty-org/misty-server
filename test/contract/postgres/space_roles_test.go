package db

import (
	"context"
	"errors"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	. "github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/spaces"
)

// rolesFixture is a Space with its owner and two members, all joined.
type rolesFixture struct {
	database *Database
	ctx      context.Context
	space    *Space
	owner    *User
	crew     *User
	rest     *User
}

func newRolesFixture(t *testing.T, prefix string) rolesFixture {
	t.Helper()
	database := openTestDatabase(t)
	ctx := context.Background()
	users := make([]*User, 3)
	for index, name := range []string{"owner", "crew", "rest"} {
		user, err := database.CreateUser(prefix+"-"+name+"@example.com", "password123")
		if err != nil {
			t.Fatalf("CreateUser(%s) error = %v", name, err)
		}
		users[index] = user
	}
	space := createTestSpace(t, database, ctx, users[0].ID, "Roles "+prefix)
	for _, member := range users[1:] {
		invite, err := database.TestingInviteToSpace(ctx, users[0].ID, space.ID, member.Email)
		if err != nil {
			t.Fatalf("InviteToSpace(%s) error = %v", member.Email, err)
		}
		if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
			t.Fatalf("RespondToSpaceInvite(%s) error = %v", member.Email, err)
		}
	}
	return rolesFixture{database: database, ctx: ctx, space: space, owner: users[0], crew: users[1], rest: users[2]}
}

func (f rolesFixture) role(t *testing.T, name string, permissions ...string) *SpaceRole {
	t.Helper()
	if permissions == nil {
		permissions = []string{}
	}
	role, err := f.database.CreateSpaceRole(f.ctx, f.owner.ID, f.space.ID, SpaceRoleInput{Name: &name, Permissions: &permissions})
	if err != nil {
		t.Fatalf("CreateSpaceRole(%s) error = %v", name, err)
	}
	return role
}

func (f rolesFixture) everyone(t *testing.T) SpaceRole {
	t.Helper()
	roles, err := f.database.SpaceRoles(f.ctx, f.rest.ID, f.space.ID)
	if err != nil {
		t.Fatalf("SpaceRoles() error = %v", err)
	}
	for _, role := range roles {
		if role.IsDefault {
			return role
		}
	}
	t.Fatalf("roles = %#v, want an everyone role", roles)
	return SpaceRole{}
}

func TestNewSpaceStartsWithTheEveryoneRoleAndMembersKeepTodaysPermissions(t *testing.T) {
	f := newRolesFixture(t, "roles-seed")
	everyone := f.everyone(t)
	if everyone.Name != "everyone" || everyone.Position != 0 || everyone.Color != "" ||
		!reflect.DeepEqual(everyone.Permissions, spaces.DefaultEveryonePermissions()) {
		t.Fatalf("everyone role = %#v, want the default grant at position 0", everyone)
	}
	owned, err := f.database.SpaceByID(f.ctx, f.owner.ID, f.space.ID)
	if err != nil || !reflect.DeepEqual(owned.Permissions, spaces.OwnerPermissions().List()) {
		t.Fatalf("owner's Space permissions = %v, %v", owned.Permissions, err)
	}
	joined, err := f.database.SpaceByID(f.ctx, f.rest.ID, f.space.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := spaces.BasePermissions([]spaces.Role{{IsDefault: true, Permissions: spaces.DefaultEveryonePermissions()}}).List()
	if !reflect.DeepEqual(joined.Permissions, want) {
		t.Fatalf("member's Space permissions = %v, want today's fixed list and leave", joined.Permissions)
	}
	// The everyone role keeps its name, and nobody can delete it.
	other := "Everybody"
	if _, err := f.database.UpdateSpaceRole(f.ctx, f.owner.ID, f.space.ID, everyone.ID, SpaceRoleInput{Name: &other}); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("renaming everyone error = %v, want ErrSpaceInvalid", err)
	}
	if err := f.database.DeleteSpaceRole(f.ctx, f.owner.ID, f.space.ID, everyone.ID); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("deleting everyone error = %v, want ErrSpaceInvalid", err)
	}
}

// The migration seeds every existing Space with the same grant new Spaces get.
func TestRolesMigrationSeedsTheDefaultEveryoneGrant(t *testing.T) {
	raw, err := os.ReadFile("../../../internal/platform/db/migrations/20271220000000_space_roles.sql")
	if err != nil {
		t.Fatal(err)
	}
	seed := regexp.MustCompile(`(?s)SELECT s\.id, 'everyone', 0, ARRAY\[(.*?)\]::text\[\], true`).FindSubmatch(raw)
	if seed == nil {
		t.Fatal("the migration doesn't seed an everyone role per Space")
	}
	seeded := []string{}
	for _, item := range strings.Split(string(seed[1]), ",") {
		seeded = append(seeded, strings.Trim(strings.TrimSpace(item), "'"))
	}
	// Later migrations add to every existing everyone role what new Spaces start with.
	for _, later := range []string{"20271225000000_space_drive.sql"} {
		raw, err := os.ReadFile("../../../internal/platform/db/migrations/" + later)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		for _, added := range regexp.MustCompile(`array_append\(permissions, '([a-z_.]+)'\)[^;]*WHERE is_default`).FindAllStringSubmatch(up, -1) {
			seeded = append(seeded, added[1])
		}
	}
	// And take out of every role what Misty no longer has.
	for _, later := range []string{"20280125000000_remove_library_items.sql"} {
		raw, err := os.ReadFile("../../../internal/platform/db/migrations/" + later)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		removed := map[string]bool{}
		for _, list := range regexp.MustCompile(`WHERE p NOT IN \(([^)]*)\)`).FindAllStringSubmatch(up, -1) {
			for _, item := range strings.Split(list[1], ",") {
				removed[strings.Trim(strings.TrimSpace(item), "'")] = true
			}
		}
		kept := []string{}
		for _, permission := range seeded {
			if !removed[permission] {
				kept = append(kept, permission)
			}
		}
		seeded = kept
	}
	if !reflect.DeepEqual(seeded, spaces.DefaultEveryonePermissions()) {
		t.Fatalf("migration seeds %v, want %v", seeded, spaces.DefaultEveryonePermissions())
	}
}

func TestRoleHierarchyLimitsWhatAManagerCanTouch(t *testing.T) {
	f := newRolesFixture(t, "roles-hierarchy")
	low := f.role(t, "Low")
	manager := f.role(t, "Manager", spaces.PermissionRolesManage, spaces.PermissionMembersRemove)
	top := f.role(t, "Top")
	// New roles land at the bottom: Top 1, Manager 2, Low 3. Put Top first, then Manager, then Low.
	if _, err := f.database.ReorderSpaceRoles(f.ctx, f.owner.ID, f.space.ID, []string{top.ID, manager.ID, low.ID}); err != nil {
		t.Fatalf("ReorderSpaceRoles(owner) error = %v", err)
	}
	if _, err := f.database.SetSpaceMemberRoles(f.ctx, f.owner.ID, f.space.ID, f.crew.ID, []string{manager.ID}); err != nil {
		t.Fatalf("SetSpaceMemberRoles(owner) error = %v", err)
	}
	color := "#A0C4D4"
	if updated, err := f.database.UpdateSpaceRole(f.ctx, f.crew.ID, f.space.ID, low.ID, SpaceRoleInput{Color: &color}); err != nil || updated.Color != "#a0c4d4" {
		t.Fatalf("UpdateSpaceRole(below) = %#v, %v", updated, err)
	}
	if _, err := f.database.UpdateSpaceRole(f.ctx, f.crew.ID, f.space.ID, top.ID, SpaceRoleInput{Color: &color}); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("editing a role above the manager's error = %v, want ErrSpaceForbidden", err)
	}
	grant := []string{spaces.PermissionMessagesManage}
	if _, err := f.database.UpdateSpaceRole(f.ctx, f.crew.ID, f.space.ID, low.ID, SpaceRoleInput{Permissions: &grant}); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("granting a permission the manager lacks error = %v, want ErrSpaceForbidden", err)
	}
	if _, err := f.database.ReorderSpaceRoles(f.ctx, f.crew.ID, f.space.ID, []string{top.ID, low.ID, manager.ID}); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("moving a role up past the manager's error = %v, want ErrSpaceForbidden", err)
	}
	// Assigning skips roles the manager can't touch and keeps them as they were.
	member, err := f.database.SetSpaceMemberRoles(f.ctx, f.crew.ID, f.space.ID, f.rest.ID, []string{low.ID, top.ID})
	if err != nil || !reflect.DeepEqual(member.RoleIDs, []string{low.ID}) {
		t.Fatalf("SetSpaceMemberRoles(manager) = %#v, %v, want just Low", member, err)
	}
	if err := f.database.RemoveSpaceMember(f.ctx, f.crew.ID, f.space.ID, f.owner.ID); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("removing the owner error = %v, want ErrSpaceForbidden", err)
	}
	if err := f.database.RemoveSpaceMember(f.ctx, f.crew.ID, f.space.ID, f.rest.ID); err != nil {
		t.Fatalf("removing a member below error = %v", err)
	}
}

func TestPrivateChannelIsInvisibleToThoseWithoutRead(t *testing.T) {
	f := newRolesFixture(t, "roles-private")
	crewRole := f.role(t, "Crew")
	if _, err := f.database.SetSpaceMemberRoles(f.ctx, f.owner.ID, f.space.ID, f.crew.ID, []string{crewRole.ID}); err != nil {
		t.Fatal(err)
	}
	everyone := f.everyone(t)
	channel, err := f.database.CreateSpaceConversationWithOverwrites(f.ctx, f.owner.ID, f.space.ID, "Crew room", nil, []SpaceConversationOverwrite{
		{RoleID: everyone.ID, Deny: []string{spaces.PermissionMessagesRead}},
		{RoleID: crewRole.ID, Allow: []string{spaces.PermissionMessagesRead}},
	})
	if err != nil {
		t.Fatalf("CreateSpaceConversationWithOverwrites() error = %v", err)
	}
	if len(channel.Participants) != 2 || len(channel.Overwrites) != 2 {
		t.Fatalf("private channel = %#v, want the owner and the crew in it, with two overwrites", channel)
	}
	if _, err := f.database.CreateSpaceConversation(f.ctx, f.crew.ID, f.space.ID, "Mine", nil); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("a member without channels.manage made a channel: %v", err)
	}
	var cursor int64
	if events, _, err := f.database.SpaceEventsAfter(f.ctx, f.rest.ID, 0, 1000); err == nil && len(events) > 0 {
		cursor = events[len(events)-1].ID
	}
	content := []MessageSpan{{Type: "text", Text: "launch codes"}}
	message, _, err := f.database.TestingCreateSpaceConversationMessageWithReferences(f.ctx, f.crew.ID, f.space.ID, channel.ID, content, nil, nil, "")
	if err != nil {
		t.Fatalf("crew posting in the private channel error = %v", err)
	}

	listed, err := f.database.SpaceConversations(f.ctx, f.rest.ID, f.space.ID)
	if err != nil || len(listed) != 0 {
		t.Fatalf("SpaceConversations(rest) = %#v, %v, want none", listed, err)
	}
	if _, err := f.database.SpaceConversationMessages(f.ctx, f.rest.ID, f.space.ID, channel.ID, 0, 20); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("SpaceConversationMessages(rest) error = %v, want ErrSpaceNotFound", err)
	}
	if _, _, err := f.database.TestingCreateSpaceConversationMessageWithReferences(f.ctx, f.rest.ID, f.space.ID, channel.ID, content, nil, nil, ""); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("posting into a hidden channel error = %v, want ErrSpaceNotFound", err)
	}
	if hits, err := f.database.SearchSpaceMessages(f.ctx, f.rest.ID, f.space.ID, "launch", 20); err != nil || len(hits) != 0 {
		t.Fatalf("SearchSpaceMessages(rest) = %#v, %v, want nothing", hits, err)
	}
	if hits, err := f.database.SearchSpaceMessages(f.ctx, f.crew.ID, f.space.ID, "launch", 20); err != nil || len(hits) != 1 {
		t.Fatalf("SearchSpaceMessages(crew) = %#v, %v, want the message", hits, err)
	}
	events, _, err := f.database.SpaceEventsAfter(f.ctx, f.rest.ID, cursor, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if containsSpaceEvent(events, "message.created", message.ID) {
		t.Fatal("a member without read got the private channel's message")
	}

	// Taking the crew's allow away removes the channel from them, and only them.
	crewCursor := int64(0)
	if crewEvents, _, err := f.database.SpaceEventsAfter(f.ctx, f.crew.ID, 0, 1000); err == nil && len(crewEvents) > 0 {
		crewCursor = crewEvents[len(crewEvents)-1].ID
	}
	updated, err := f.database.SetSpaceConversationOverwrites(f.ctx, f.owner.ID, f.space.ID, channel.ID, []SpaceConversationOverwrite{
		{RoleID: everyone.ID, Deny: []string{spaces.PermissionMessagesRead}},
	})
	if err != nil || len(updated.Participants) != 1 {
		t.Fatalf("SetSpaceConversationOverwrites() = %#v, %v, want only the owner left in it", updated, err)
	}
	crewEvents, _, err := f.database.SpaceEventsAfter(f.ctx, f.crew.ID, crewCursor, 1000)
	if err != nil || !containsSpaceEvent(crewEvents, "conversation.removed", channel.ID) || containsSpaceEvent(crewEvents, "conversation.updated", channel.ID) {
		t.Fatalf("crew events = %#v, %v, want conversation.removed and no update", crewEvents, err)
	}
	restEvents, _, err := f.database.SpaceEventsAfter(f.ctx, f.rest.ID, cursor, 1000)
	if err != nil || containsSpaceEvent(restEvents, "conversation.removed", channel.ID) {
		t.Fatalf("rest events = %#v, %v, want no removal of a channel they never had", restEvents, err)
	}
}

func TestReadOnlyChannelAndModeratorDeletes(t *testing.T) {
	f := newRolesFixture(t, "roles-moderation")
	moderator := f.role(t, "Moderator", spaces.PermissionMessagesManage)
	if _, err := f.database.SetSpaceMemberRoles(f.ctx, f.owner.ID, f.space.ID, f.crew.ID, []string{moderator.ID}); err != nil {
		t.Fatal(err)
	}
	everyone := f.everyone(t)
	channel, err := f.database.CreateSpaceConversation(f.ctx, f.owner.ID, f.space.ID, "News", nil)
	if err != nil {
		t.Fatal(err)
	}
	content := []MessageSpan{{Type: "text", Text: "hello"}}
	posted, _, err := f.database.TestingCreateSpaceConversationMessageWithReferences(f.ctx, f.rest.ID, f.space.ID, channel.ID, content, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.database.DeleteSpaceConversationMessage(f.ctx, f.owner.ID, f.space.ID, channel.ID, "missing"); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("deleting a missing message error = %v, want ErrSpaceNotFound", err)
	}
	if err := f.database.DeleteSpaceConversationMessage(f.ctx, f.crew.ID, f.space.ID, channel.ID, posted.ID); err != nil {
		t.Fatalf("a moderator deleting someone else's message error = %v", err)
	}
	if _, err := f.database.SetSpaceConversationOverwrites(f.ctx, f.owner.ID, f.space.ID, channel.ID, []SpaceConversationOverwrite{
		{RoleID: everyone.ID, Deny: []string{spaces.PermissionMessagesWrite}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.database.TestingCreateSpaceConversationMessageWithReferences(f.ctx, f.rest.ID, f.space.ID, channel.ID, content, nil, nil, ""); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("posting in a read-only channel error = %v, want ErrSpaceForbidden", err)
	}
	if _, _, err := f.database.TestingCreateSpaceConversationMessageWithReferences(f.ctx, f.owner.ID, f.space.ID, channel.ID, content, nil, nil, ""); err != nil {
		t.Fatalf("the owner posting in a read-only channel error = %v", err)
	}
	listed, err := f.database.SpaceConversations(f.ctx, f.rest.ID, f.space.ID)
	if err != nil || len(listed) != 1 || slices.Contains(listed[0].Permissions, spaces.PermissionMessagesWrite) {
		t.Fatalf("SpaceConversations(rest) = %#v, %v, want the channel without write", listed, err)
	}
}
