package spaces

import "time"

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
	PermissionStorageViewMembers = "storage.view_member_usage"
	PermissionStorageManage      = "storage.manage"
	PermissionStorageViewOwn     = "storage.view_own_usage"
	PermissionStudioView         = "studio.view"
	PermissionStudioManage       = "studio.manage"
	PermissionAskRun             = "ask.run"
	PermissionTasksView          = "tasks.view"
	PermissionTasksManage        = "tasks.manage"
	PermissionIntegrationsManage = "integrations.manage"
	PermissionSpaceInvite        = "space.invite"
	PermissionSpaceRename        = "space.rename"
	PermissionSpaceTransfer      = "space.transfer"
	PermissionSpaceDelete        = "space.delete"
	PermissionSpaceLeave         = "space.leave"
	LibraryRecoveryWindow        = 30 * 24 * time.Hour
)

const (
	UploadPurposeLibrary        = "library"
	UploadPurposeChatAttachment = "attachment"
	UploadPurposeNoteAttachment = "note_attachment"
	UploadPurposeDrawingAsset   = "drawing_attachment"
)

const (
	DefaultLibraryMaxFileBytes        = int64(100 << 20)
	DefaultChatAttachmentMaxFileBytes = int64(10 << 20)
	DefaultNoteAttachmentMaxFileBytes = int64(15 << 20)
	DefaultDrawingAssetMaxFileBytes   = int64(15 << 20)
)

func MaxUploadBytesForPurpose(purpose string) int64 {
	switch purpose {
	case UploadPurposeLibrary:
		return DefaultLibraryMaxFileBytes
	case UploadPurposeChatAttachment:
		return DefaultChatAttachmentMaxFileBytes
	case UploadPurposeNoteAttachment:
		return DefaultNoteAttachmentMaxFileBytes
	case UploadPurposeDrawingAsset:
		return DefaultDrawingAssetMaxFileBytes
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
	case UploadPurposeDrawingAsset:
		// Drawing assets authorize against the parent drawing.
		return PermissionLibraryView, true
	default:
		return "", false
	}
}

var configurableSpacePermissions = []string{
	PermissionMessagesRead, PermissionMessagesWrite,
	PermissionLibraryView, PermissionLibraryUpload, PermissionAttachmentUpload,
	PermissionLibraryAdd, PermissionLibraryEdit, PermissionLibraryDownload,
	PermissionLibraryImport, PermissionStorageViewOwn, PermissionStorageViewMembers,
	PermissionStorageManage, PermissionStudioView, PermissionStudioManage, PermissionAskRun,
	PermissionTasksView, PermissionTasksManage, PermissionIntegrationsManage,
}

func ConfigurablePermissions() []string { return append([]string{}, configurableSpacePermissions...) }
func ApplyPermissionDependencies(permissions map[string]bool) {
	if !permissions[PermissionMessagesRead] {
		permissions[PermissionMessagesWrite] = false
	}
	if !permissions[PermissionMessagesRead] || !permissions[PermissionMessagesWrite] {
		permissions[PermissionAttachmentUpload] = false
	}
	if !permissions[PermissionTasksView] {
		permissions[PermissionTasksManage] = false
	}
}
