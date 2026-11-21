package spaces

import "math"

const (
	PermissionMessagesRead       = "messages.read"
	PermissionMessagesWrite      = "messages.write"
	PermissionAttachmentUpload   = "attachments.upload"
	PermissionStorageViewOwn     = "storage.view_own_usage"
	PermissionStudioView         = "studio.view"
	PermissionAskRun             = "ask.run"
	PermissionIntegrationsManage = "integrations.manage"
	// PermissionAppsManage lets a member install and remove the Space's apps: a Space's mods.
	PermissionAppsManage = "apps.manage"
	// PermissionDriveWrite lets a member change the Space's drive: add, change, move, trash and
	// restore its items, and mark them in use. Reading the drive needs membership only.
	PermissionDriveWrite = "drive.write"
	// PermissionDrivePurge lets a member delete trashed drive items for good.
	PermissionDrivePurge    = "drive.purge"
	PermissionSpaceInvite   = "space.invite"
	PermissionSpaceRename   = "space.rename"
	PermissionSpaceTransfer = "space.transfer"
	PermissionSpaceDelete   = "space.delete"
	PermissionSpaceLeave    = "space.leave"

	// Roles: administrator holds every permission but the owner's own, and
	// ignores channel overwrites.
	PermissionAdministrator  = "administrator"
	PermissionRolesManage    = "roles.manage"
	PermissionChannelsManage = "channels.manage"
	PermissionMessagesManage = "messages.manage"
	PermissionMembersRemove  = "members.remove"
)

// retiredPermissions gated a Space's Library items, which are gone. A role
// that still names one (an older app saving it) has it dropped, not refused.
var retiredPermissions = map[string]bool{
	"library.view": true, "library.upload": true, "library.add": true,
	"library.edit": true, "library.download": true, "library.import": true,
}

// IsRetiredPermission names a permission Misty no longer has.
func IsRetiredPermission(permission string) bool { return retiredPermissions[permission] }

const (
	UploadPurposeChatAttachment = "attachment"
	UploadPurposeNoteAttachment = "note_attachment"
	// UploadPurposeDrive is one file's bytes in the Space's drive.
	UploadPurposeDrive = "drive"
)

const (
	DefaultChatAttachmentMaxFileBytes = int64(10 << 20)
	DefaultNoteAttachmentMaxFileBytes = int64(15 << 20)
)

func MaxUploadBytesForPurpose(purpose string) int64 {
	switch purpose {
	case UploadPurposeChatAttachment:
		return DefaultChatAttachmentMaxFileBytes
	case UploadPurposeNoteAttachment:
		return DefaultNoteAttachmentMaxFileBytes
	case UploadPurposeDrive:
		// No cap of its own: drive files count against the storage limits and
		// the Space's upload size, and the drive keeps its own per-object bound.
		return math.MaxInt64
	default:
		return 0
	}
}

// UploadPurposePermission maps a purpose to the Space permission the generic
// upload route requires. Note assets have none: they authorize against the
// parent note, through the note routes only.
func UploadPurposePermission(purpose string) (string, bool) {
	switch purpose {
	case UploadPurposeChatAttachment:
		return PermissionAttachmentUpload, true
	case UploadPurposeDrive:
		return PermissionDriveWrite, true
	default:
		return "", false
	}
}
