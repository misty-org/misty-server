package spaces

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	DefaultRoleName  = "everyone"
	MaxRoleNameRunes = 64
	MaxRolesPerSpace = 250
)

type Role struct {
	ID          string
	Position    int
	Permissions []string
	IsDefault   bool
}

type Overwrite struct {
	RoleID string
	Allow  []string
	Deny   []string
}

type PermissionSet map[string]bool

func (s PermissionSet) Has(permission string) bool { return s[permission] }

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

var allPermissions = []string{
	PermissionAdministrator,
	PermissionMessagesRead, PermissionMessagesWrite, PermissionAttachmentUpload, PermissionMessagesManage,
	PermissionStorageViewOwn, PermissionStudioView,
	PermissionDriveWrite, PermissionDrivePurge,
	PermissionAskRun, PermissionIntegrationsManage, PermissionAppsManage, PermissionChannelsManage, PermissionRolesManage,
	PermissionMembersRemove, PermissionSpaceInvite, PermissionSpaceRename, PermissionSpaceLeave,
	PermissionSpaceTransfer, PermissionSpaceDelete,
}

var channelPermissions = []string{
	PermissionMessagesRead, PermissionMessagesWrite, PermissionAttachmentUpload, PermissionMessagesManage,
}

var defaultEveryonePermissions = []string{
	PermissionMessagesRead, PermissionMessagesWrite, PermissionAttachmentUpload,
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

func IsOwnerOnlyPermission(permission string) bool {
	return permission == PermissionSpaceTransfer || permission == PermissionSpaceDelete
}

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

func OwnerPermissions() PermissionSet {
	out := permissionSetOf(allPermissions)
	delete(out, PermissionSpaceLeave)
	return out
}

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

func DirectPermissions(base PermissionSet) PermissionSet {
	out := PermissionSet{}
	for _, permission := range channelPermissions {
		if base[permission] {
			out[permission] = true
		}
	}
	return out
}

func HighestPosition(roles []Role) int {
	highest := 0
	for _, role := range roles {
		if role.Position > highest {
			highest = role.Position
		}
	}
	return highest
}

func CanManageRole(actorIsOwner bool, actorHighest, rolePosition int) bool {
	return actorIsOwner || rolePosition < actorHighest
}

func CanRemoveMember(actorIsOwner bool, actorHighest int, targetIsOwner bool, targetHighest int) bool {
	if targetIsOwner {
		return false
	}
	return actorIsOwner || targetHighest < actorHighest
}

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

func NormalizeRoleName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	length := utf8.RuneCountInString(name)
	return name, length >= 1 && length <= MaxRoleNameRunes && !strings.EqualFold(name, DefaultRoleName)
}

func NormalizeRoleColor(color string) (string, bool) {
	color = strings.ToLower(strings.TrimSpace(color))
	return color, color == "" || roleColorPattern.MatchString(color)
}

func NormalizeRolePermissions(permissions []string) ([]string, bool) {
	set := PermissionSet{}
	for _, permission := range permissions {
		if IsRetiredPermission(permission) {
			continue
		}
		if !IsAssignablePermission(permission) {
			return nil, false
		}
		set[permission] = true
	}
	return set.List(), true
}

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
