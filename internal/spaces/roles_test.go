package spaces

import (
	"reflect"
	"testing"
)

func TestBasePermissionsUnionRolesAndAdministratorBringsAllButOwnerOnly(t *testing.T) {
	everyone := Role{ID: "everyone", IsDefault: true, Permissions: []string{PermissionMessagesRead}}
	moderator := Role{ID: "mod", Position: 2, Permissions: []string{PermissionMessagesManage, PermissionSpaceDelete}}
	base := BasePermissions([]Role{everyone, moderator})
	if !base.Has(PermissionMessagesRead) || !base.Has(PermissionMessagesManage) || !base.Has(PermissionSpaceLeave) {
		t.Fatalf("base = %v, want everyone's and the moderator's permissions and leave", base.List())
	}
	if base.Has(PermissionSpaceDelete) {
		t.Fatal("a role carried space.delete, which is the owner's alone")
	}
	admin := BasePermissions([]Role{everyone, {ID: "admin", Position: 3, Permissions: []string{PermissionAdministrator}}})
	for _, permission := range AllPermissions() {
		if admin.Has(permission) == IsOwnerOnlyPermission(permission) {
			t.Errorf("administrator has %s = %v", permission, admin.Has(permission))
		}
	}
	owner := OwnerPermissions()
	if owner.Has(PermissionSpaceLeave) || !owner.Has(PermissionSpaceTransfer) || !owner.Has(PermissionAdministrator) {
		t.Fatalf("owner = %v, want everything but leaving", owner.List())
	}
}

func TestChannelPermissionsApplyEveryoneThenDenyThenAllow(t *testing.T) {
	base := BasePermissions([]Role{{ID: "everyone", IsDefault: true, Permissions: DefaultEveryonePermissions()}})
	private := []Overwrite{
		{RoleID: "everyone", Deny: []string{PermissionMessagesRead}},
		{RoleID: "crew", Allow: []string{PermissionMessagesRead}},
		{RoleID: "muted", Deny: []string{PermissionMessagesWrite}},
		{RoleID: "speaker", Allow: []string{PermissionMessagesWrite}},
	}
	outsider := ChannelPermissions(base, "everyone", nil, private)
	for _, permission := range []string{PermissionMessagesRead, PermissionMessagesWrite, PermissionAttachmentUpload} {
		if outsider.Has(permission) {
			t.Errorf("outsider holds %s in a private channel", permission)
		}
	}
	if !outsider.Has(PermissionLibraryView) {
		t.Error("an overwrite changed a permission that isn't channel-scoped")
	}
	crew := ChannelPermissions(base, "everyone", []string{"crew"}, private)
	if !crew.Has(PermissionMessagesRead) || !crew.Has(PermissionMessagesWrite) {
		t.Fatalf("crew = %v, want read and write", crew.List())
	}
	muted := ChannelPermissions(base, "everyone", []string{"crew", "muted"}, private)
	if !muted.Has(PermissionMessagesRead) || muted.Has(PermissionMessagesWrite) {
		t.Fatalf("muted = %v, want read without write", muted.List())
	}
	// An allow on any role wins over a deny on another.
	both := ChannelPermissions(base, "everyone", []string{"crew", "muted", "speaker"}, private)
	if !both.Has(PermissionMessagesWrite) {
		t.Fatalf("muted speaker = %v, want write", both.List())
	}
	admin := BasePermissions([]Role{{ID: "admin", Permissions: []string{PermissionAdministrator}}})
	if !ChannelPermissions(admin, "everyone", nil, private).Has(PermissionMessagesRead) {
		t.Fatal("an administrator was kept out by an overwrite")
	}
}

func TestChannelPermissionsWithoutReadGrantNothingInTheChannel(t *testing.T) {
	base := BasePermissions([]Role{{ID: "everyone", IsDefault: true, Permissions: DefaultEveryonePermissions()}})
	hidden := ChannelPermissions(base, "everyone", nil, []Overwrite{
		{RoleID: "everyone", Allow: []string{PermissionMessagesManage}, Deny: []string{PermissionMessagesRead}},
	})
	for _, permission := range []string{PermissionMessagesWrite, PermissionMessagesManage, PermissionAttachmentUpload} {
		if hidden.Has(permission) {
			t.Errorf("a channel without read still grants %s", permission)
		}
	}
}

func TestDirectPermissionsKeepOnlyChannelScopedOnes(t *testing.T) {
	got := DirectPermissions(OwnerPermissions()).List()
	want := []string{PermissionMessagesRead, PermissionMessagesWrite, PermissionAttachmentUpload, PermissionMessagesManage}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DirectPermissions(owner) = %v, want %v", got, want)
	}
}

func TestRoleHierarchy(t *testing.T) {
	if !CanManageRole(false, 3, 2) || CanManageRole(false, 3, 3) || CanManageRole(false, 3, 4) {
		t.Fatal("a member manages exactly the roles below their highest")
	}
	if !CanManageRole(true, 0, 99) {
		t.Fatal("the owner is above every role")
	}
	if CanRemoveMember(false, 3, false, 3) || !CanRemoveMember(false, 3, false, 2) {
		t.Fatal("a member removes exactly those whose highest role is below theirs")
	}
	if CanRemoveMember(true, 0, true, 0) || CanRemoveMember(false, 9, true, 0) {
		t.Fatal("nobody removes the owner")
	}
	before := map[string]int{"a": 1, "b": 2, "mine": 3, "top": 4}
	if !ReorderAllowed(false, 3, before, map[string]int{"a": 2, "b": 1, "mine": 3, "top": 4}) {
		t.Fatal("swapping two roles below the actor's highest was refused")
	}
	if ReorderAllowed(false, 3, before, map[string]int{"a": 3, "b": 2, "mine": 1, "top": 4}) {
		t.Fatal("a role was moved up to the actor's own position")
	}
	if !ReorderAllowed(true, 0, before, map[string]int{"a": 4, "b": 3, "mine": 2, "top": 1}) {
		t.Fatal("the owner may reorder everything")
	}
}

func TestHoldsChangesChecksOnlyWhatChanged(t *testing.T) {
	actor := permissionSetOf([]string{PermissionMessagesRead, PermissionRolesManage})
	if !HoldsChanges(actor, []string{PermissionAdministrator}, []string{PermissionAdministrator, PermissionMessagesRead}) {
		t.Fatal("keeping a permission the actor lacks counted as handing it out")
	}
	if HoldsChanges(actor, nil, []string{PermissionMessagesManage}) {
		t.Fatal("granted a permission the actor lacks")
	}
	if HoldsChanges(actor, []string{PermissionMessagesManage}, nil) {
		t.Fatal("took away a permission the actor lacks")
	}
	before := []Overwrite{{RoleID: "everyone", Deny: []string{PermissionMessagesManage}}}
	after := []Overwrite{{RoleID: "everyone", Deny: []string{PermissionMessagesManage, PermissionMessagesRead}}}
	if !OverwriteChangesHeld(actor, before, after) {
		t.Fatal("denying read, which the actor holds, was refused")
	}
	if OverwriteChangesHeld(actor, before, nil) {
		t.Fatal("cleared an overwrite of a permission the actor lacks")
	}
}

func TestNormalizeRolesAndOverwrites(t *testing.T) {
	if _, ok := NormalizeRoleName("  Everyone "); ok {
		t.Error("a second role passed for everyone")
	}
	if name, ok := NormalizeRoleName("  Moderator "); !ok || name != "Moderator" {
		t.Errorf("NormalizeRoleName = %q, %v", name, ok)
	}
	if color, ok := NormalizeRoleColor("#A0C4D4"); !ok || color != "#a0c4d4" {
		t.Errorf("NormalizeRoleColor = %q, %v", color, ok)
	}
	if _, ok := NormalizeRoleColor("red"); ok {
		t.Error("accepted a color that isn't #rrggbb")
	}
	for _, permission := range []string{PermissionSpaceDelete, PermissionSpaceTransfer, PermissionSpaceLeave, "messages.everything"} {
		if _, ok := NormalizeRolePermissions([]string{permission}); ok {
			t.Errorf("a role carried %s", permission)
		}
	}
	permissions, ok := NormalizeRolePermissions([]string{PermissionMessagesWrite, PermissionMessagesRead, PermissionMessagesWrite})
	if !ok || !reflect.DeepEqual(permissions, []string{PermissionMessagesRead, PermissionMessagesWrite}) {
		t.Errorf("NormalizeRolePermissions = %v, %v", permissions, ok)
	}
	for _, bad := range [][]Overwrite{
		{{RoleID: "a", Allow: []string{PermissionLibraryView}}},
		{{RoleID: "a", Allow: []string{PermissionMessagesRead}, Deny: []string{PermissionMessagesRead}}},
		{{RoleID: "a", Allow: []string{PermissionMessagesRead}}, {RoleID: "a", Deny: []string{PermissionMessagesWrite}}},
		{{RoleID: " ", Allow: []string{PermissionMessagesRead}}},
	} {
		if _, ok := NormalizeOverwrites(bad); ok {
			t.Errorf("NormalizeOverwrites accepted %#v", bad)
		}
	}
	got, ok := NormalizeOverwrites([]Overwrite{{RoleID: "a"}, {RoleID: "b", Deny: []string{PermissionMessagesRead}}})
	if !ok || len(got) != 1 || got[0].RoleID != "b" || len(got[0].Allow) != 0 {
		t.Errorf("NormalizeOverwrites = %#v, %v, want only b's", got, ok)
	}
}
