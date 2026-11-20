package spaces

import (
	"math"
	"time"
)

const (
	PermissionLibraryView        = "library.view"
	PermissionMessagesRead       = "messages.read"
	PermissionMessagesWrite      = "messages.write"
	PermissionLibraryUpload      = "library.upload"
	PermissionAttachmentUpload   = "attachments.upload"
	PermissionLibraryAdd         = "library.add"
	PermissionLibraryEdit        = "library.edit"
	PermissionLibraryDownload    = "library.download"
	PermissionLibraryImport      = "library.import"
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
	LibraryRecoveryWindow    = 30 * 24 * time.Hour
)

const (
	UploadPurposeLibrary        = "library"
	UploadPurposeChatAttachment = "attachment"
	UploadPurposeNoteAttachment = "note_attachment"
	// UploadPurposeDrive is one file's bytes in the Space's drive.
	UploadPurposeDrive = "drive"
)

const (
	DefaultLibraryMaxFileBytes        = int64(100 << 20)
	DefaultChatAttachmentMaxFileBytes = int64(10 << 20)
	DefaultNoteAttachmentMaxFileBytes = int64(15 << 20)
)

func MaxUploadBytesForPurpose(purpose string) int64 {
	switch purpose {
	case UploadPurposeLibrary:
		return DefaultLibraryMaxFileBytes
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

// UploadPurposePermission maps a purpose to the Space permission it requires.

func UploadPurposePermission(purpose string) (string, bool) {
	switch purpose {
	case UploadPurposeLibrary:
		return PermissionLibraryUpload, true
	case UploadPurposeChatAttachment:
		return PermissionAttachmentUpload, true
	case UploadPurposeNoteAttachment:
		// Note assets authorize against the parent note, not a Space-wide
		// permission. Callers must check note edit access before reaching here.
		return PermissionLibraryView, true
	case UploadPurposeDrive:
		return PermissionDriveWrite, true
	default:
		return "", false
	}
}
