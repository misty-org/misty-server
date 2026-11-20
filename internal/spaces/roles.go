package spaces

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Space roles follow Discord's model (as Fluxer implements it): every member
// holds the default role, everyone, plus any roles they were given; a member's
// Space-wide permissions are the union of those roles' permissions; and a
// standard channel can overwrite the channel-scoped ones per role.

const (
	// DefaultRoleName is the name of the role every member holds.
	DefaultRoleName = "everyone"
	// MaxRoleNameRunes keeps a role's name short enough for a member list.
	MaxRoleNameRunes = 64
	// MaxRolesPerSpace bounds a Space's roles, the default one included.
	MaxRolesPerSpace = 250
)

// Role is what permission math needs to know about one of a Space's roles.
// The default role has position 0; every other role is above it.
type Role struct {
	ID          string
	Position    int
	Permissions []string
	IsDefault   bool
}

// Overwrite changes channel-scoped permissions for one role in one channel.
type Overwrite struct {
	RoleID string
	Allow  []string
	Deny   []string
}

// PermissionSet is a set of permission strings.
type PermissionSet map[string]bool

func (s PermissionSet) Has(permission string) bool { return s[permission] }

// List returns the set in the order apps show permissions. It is never nil,
// so it reaches JSON as [] rather than null.
func (s PermissionSet) List() []string {
	out := []string{}
	for _, permission := range allPermissions {
		if s[permission] {
			out = append(out, permission)
		}
	}
	return out
}

func (s PermissionSet) clone() PermissionSet {
	out := make(PermissionSet, len(s))
	for permission, held := range s {
		if held {
			out[permission] = true
		}
	}
	return out
}

func permissionSetOf(values []string) PermissionSet {
	out := make(PermissionSet, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

// allPermissions is every permission, in the order apps list them.
var allPermissions = []string{
	PermissionAdministrator,
	PermissionMessagesRead, PermissionMessagesWrite, PermissionAttachmentUpload, PermissionMessagesManage,
	PermissionLibraryView, PermissionLibraryUpload, PermissionLibraryAdd, PermissionLibraryEdit,
	PermissionLibraryDownload, PermissionLibraryImport, PermissionStorageViewOwn, PermissionStudioView,
	PermissionDriveWrite, PermissionDrivePurge,
	PermissionAskRun, PermissionIntegrationsManage, PermissionAppsManage, PermissionChannelsManage, PermissionRolesManage,
	PermissionMembersRemove, PermissionSpaceInvite, PermissionSpaceRename, PermissionSpaceLeave,
	PermissionSpaceTransfer, PermissionSpaceDelete,
}

// channelPermissions are the only ones an overwrite may name.
var channelPermissions = []string{
	PermissionMessagesRead, PermissionMessagesWrite, PermissionAttachmentUpload, PermissionMessagesManage,
}

// defaultEveryonePermissions is what every member could do before roles
// existed, plus changing the Space's drive; a new Space's everyone role starts
// with it.
var defaultEveryonePermissions = []string{
	PermissionMessagesRead, PermissionMessagesWrite, PermissionAttachmentUpload,
	PermissionLibraryView, PermissionLibraryUpload, PermissionLibraryAdd,
	PermissionLibraryEdit, PermissionLibraryDownload, PermissionLibraryImport,
	PermissionStorageViewOwn, PermissionStudioView, PermissionAskRun,
	PermissionDriveWrite,
}

func AllPermissions() []string { return append([]string(nil), allPermissions...) }

func DefaultEveryonePermissions() []string {
	return append([]string(nil), defaultEveryonePermissions...)
}

func IsChannelPermission(permission string) bool {
	for _, candidate := range channelPermissions {
		if candidate == permission {
			return true
		}
	}
	return false
}

// IsOwnerOnlyPermission names what no role can grant, administrator included.
func IsOwnerOnlyPermission(permission string) bool {
	return permission == PermissionSpaceTransfer || permission == PermissionSpaceDelete
}

// IsAssignablePermission says whether a role may carry the permission. Leaving
// is every member's but the owner's, so no role carries it either.
func IsAssignablePermission(permission string) bool {
	if IsOwnerOnlyPermission(permission) || permission == PermissionSpaceLeave {
		return false
	}
	for _, candidate := range allPermissions {
		if candidate == permission {
			return true
		}
	}
	return false
}

// OwnerPermissions is everything except leaving: an owner transfers the Space first.
func OwnerPermissions() PermissionSet {
	out := permissionSetOf(allPermissions)
	delete(out, PermissionSpaceLeave)
	return out
}

// BasePermissions is a member's Space-wide set from the roles they hold, the
// everyone role included. Administrator brings every permission but the owner's own.
func BasePermissions(roles []Role) PermissionSet {
	out := PermissionSet{}
	for _, role := range roles {
		for _, permission := range role.Permissions {
			if IsAssignablePermission(permission) {
				out[permission] = true
			}
		}
	}
	if out[PermissionAdministrator] {
		for _, permission := range allPermissions {
			if !IsOwnerOnlyPermission(permission) {
				out[permission] = true
			}
		}
	}
	out[PermissionSpaceLeave] = true
	return out
}

// ChannelPermissions applies a standard channel's overwrites to a member's base
// set: the everyone role's overwrite first, then every deny of the member's
// other roles, then every allow, so an allow on any role wins over a deny on
// another. Administrators (and owners) ignore overwrites. A channel the member
// can't read grants nothing else in it either.
func ChannelPermissions(base PermissionSet, everyoneRoleID string, memberRoleIDs []string, overwrites []Overwrite) PermissionSet {
	out := base.clone()
	if base[PermissionAdministrator] {
		return out
	}
	byRole := make(map[string]Overwrite, len(overwrites))
	for _, overwrite := range overwrites {
		byRole[overwrite.RoleID] = overwrite
	}
	if overwrite, ok := byRole[everyoneRoleID]; ok && everyoneRoleID != "" {
		applyOverwrite(out, overwrite.Deny, overwrite.Allow)
	}
	var allow, deny []string
	for _, roleID := range memberRoleIDs {
		if overwrite, ok := byRole[roleID]; ok && roleID != everyoneRoleID {
			allow = append(allow, overwrite.Allow...)
			deny = append(deny, overwrite.Deny...)
		}
	}
	applyOverwrite(out, deny, allow)
	if !out[PermissionMessagesRead] {
		for _, permission := range channelPermissions {
			delete(out, permission)
		}
	}
	return out
}

func applyOverwrite(set PermissionSet, deny, allow []string) {
	for _, permission := range deny {
		if IsChannelPermission(permission) {
			delete(set, permission)
		}
	}
	for _, permission := range allow {
		if IsChannelPermission(permission) {
			set[permission] = true
		}
	}
}

// DirectPermissions is what a participant may do in a DM: overwrites don't
// apply there, and Space-wide powers like channels.manage mean nothing in it.
func DirectPermissions(base PermissionSet) PermissionSet {
	out := PermissionSet{}
	for _, permission := range channelPermissions {
		if base[permission] {
			out[permission] = true
		}
	}
	return out
}

// HighestPosition is the position of a member's highest role; everyone's is 0.
func HighestPosition(roles []Role) int {
	highest := 0
	for _, role := range roles {
		if role.Position > highest {
			highest = role.Position
		}
	}
	return highest
}

// CanManageRole: a role can be edited, deleted or assigned only from above it.
// The owner is above every role.
func CanManageRole(actorIsOwner bool, actorHighest, rolePosition int) bool {
	return actorIsOwner || rolePosition < actorHighest
}

// CanRemoveMember: never the owner, and otherwise only someone whose highest
// role is below the actor's.
func CanRemoveMember(actorIsOwner bool, actorHighest int, targetIsOwner bool, targetHighest int) bool {
	if targetIsOwner {
		return false
	}
	return actorIsOwner || targetHighest < actorHighest
}

// HoldsChanges says whether the actor holds every permission that differs
// between two grants, so nobody hands out (or takes away) what they don't have.
func HoldsChanges(actor PermissionSet, before, after []string) bool {
	old, next := permissionSetOf(before), permissionSetOf(after)
	for permission := range next {
		if !old[permission] && !actor[permission] {
			return false
		}
	}
	for permission := range old {
		if !next[permission] && !actor[permission] {
			return false
		}
	}
	return true
}

// OverwriteChangesHeld is HoldsChanges for a channel's whole set of overwrites.
func OverwriteChangesHeld(actor PermissionSet, before, after []Overwrite) bool {
	old, next := overwritesByRole(before), overwritesByRole(after)
	for roleID := range next {
		if _, ok := old[roleID]; !ok {
			old[roleID] = Overwrite{RoleID: roleID}
		}
	}
	for roleID, previous := range old {
		changed := next[roleID]
		if !HoldsChanges(actor, previous.Allow, changed.Allow) || !HoldsChanges(actor, previous.Deny, changed.Deny) {
			return false
		}
	}
	return true
}

func overwritesByRole(overwrites []Overwrite) map[string]Overwrite {
	out := make(map[string]Overwrite, len(overwrites))
	for _, overwrite := range overwrites {
		out[overwrite.RoleID] = overwrite
	}
	return out
}

// ReorderAllowed checks a new order of the non-default roles (role id to
// position, before and after). Below the owner, only roles under the actor's
// highest may move, and none may move up to it or past it.
func ReorderAllowed(actorIsOwner bool, actorHighest int, before, after map[string]int) bool {
	if actorIsOwner {
		return true
	}
	for roleID, previous := range before {
		next, ok := after[roleID]
		if !ok {
			return false
		}
		if (previous >= actorHighest || next >= actorHighest) && previous != next {
			return false
		}
	}
	return true
}

var roleColorPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// NormalizeRoleName trims a role's name and checks its length. Only the default
// role is called everyone, so no other role can pass for it.
func NormalizeRoleName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	length := utf8.RuneCountInString(name)
	return name, length >= 1 && length <= MaxRoleNameRunes && !strings.EqualFold(name, DefaultRoleName)
}

// NormalizeRoleColor accepts no color ("") or #rrggbb, stored lowercase.
func NormalizeRoleColor(color string) (string, bool) {
	color = strings.ToLower(strings.TrimSpace(color))
	return color, color == "" || roleColorPattern.MatchString(color)
}

// NormalizeRolePermissions dedupes a role's permissions into the canonical
// order. It fails on any permission a role can't carry.
func NormalizeRolePermissions(permissions []string) ([]string, bool) {
	set := PermissionSet{}
	for _, permission := range permissions {
		if !IsAssignablePermission(permission) {
			return nil, false
		}
		set[permission] = true
	}
	return set.List(), true
}

// NormalizeOverwrites checks a channel's overwrites: one per role, channel-scoped
// permissions only, none both allowed and denied. Overwrites that change
// nothing are dropped.
func NormalizeOverwrites(overwrites []Overwrite) ([]Overwrite, bool) {
	out := make([]Overwrite, 0, len(overwrites))
	seen := map[string]bool{}
	for _, overwrite := range overwrites {
		roleID := strings.TrimSpace(overwrite.RoleID)
		if roleID == "" || seen[roleID] {
			return nil, false
		}
		seen[roleID] = true
		allow, deny := PermissionSet{}, PermissionSet{}
		for _, permission := range overwrite.Allow {
			if !IsChannelPermission(permission) {
				return nil, false
			}
			allow[permission] = true
		}
		for _, permission := range overwrite.Deny {
			if !IsChannelPermission(permission) || allow[permission] {
				return nil, false
			}
			deny[permission] = true
		}
		if len(allow) == 0 && len(deny) == 0 {
			continue
		}
		out = append(out, Overwrite{RoleID: roleID, Allow: allow.List(), Deny: deny.List()})
	}
	return out, true
}
