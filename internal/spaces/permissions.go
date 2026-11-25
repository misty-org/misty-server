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
	PermissionDriveWrite         = "drive.write"
	PermissionDrivePurge         = "drive.purge"
	PermissionSpaceInvite        = "space.invite"
	PermissionSpaceRename        = "space.rename"
	PermissionSpaceTransfer      = "space.transfer"
	PermissionSpaceDelete        = "space.delete"
	PermissionSpaceLeave         = "space.leave"

	PermissionAdministrator  = "administrator"
	PermissionRolesManage    = "roles.manage"
	PermissionChannelsManage = "channels.manage"
	PermissionMessagesManage = "messages.manage"
	PermissionMembersRemove  = "members.remove"
)

var retiredPermissions = map[string]bool{
	"library.view": true, "library.upload": true, "library.add": true,
	"library.edit": true, "library.download": true, "library.import": true,
	"apps.manage": true,
}

func IsRetiredPermission(permission string) bool { return retiredPermissions[permission] }

const (
	UploadPurposeChatAttachment = "attachment"
	UploadPurposeNoteAttachment = "note_attachment"
	UploadPurposeDrive          = "drive"
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
		return math.MaxInt64
	default:
		return 0
	}
}

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
