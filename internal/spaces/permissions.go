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
	PermissionSpaceInvite        = "space.invite"
	PermissionSpaceRename        = "space.rename"
	PermissionSpaceTransfer      = "space.transfer"
	PermissionSpaceDelete        = "space.delete"
	PermissionSpaceLeave         = "space.leave"

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
	UploadPurposeDrawingAsset   = "drawing_attachment"
	// UploadPurposeDeckAsset is an image a deck's slides show, bound to the deck.
	UploadPurposeDeckAsset = "deck_attachment"
	// UploadPurposeSharedFolder is one file's bytes in a shared Space folder.
	UploadPurposeSharedFolder = "folder"
)

const (
	DefaultLibraryMaxFileBytes        = int64(100 << 20)
	DefaultChatAttachmentMaxFileBytes = int64(10 << 20)
	DefaultNoteAttachmentMaxFileBytes = int64(15 << 20)
	DefaultDrawingAssetMaxFileBytes   = int64(15 << 20)
	// DefaultDeckAssetMaxFileBytes matches a drawing's images.
	DefaultDeckAssetMaxFileBytes = DefaultDrawingAssetMaxFileBytes
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
	case UploadPurposeDeckAsset:
		return DefaultDeckAssetMaxFileBytes
	case UploadPurposeSharedFolder:
		// No cap of its own: folder bytes count against the storage limits,
		// and each object still keeps the per-object bound.
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
	case UploadPurposeDrawingAsset:
		// Drawing assets authorize against the parent drawing.
		return PermissionLibraryView, true
	case UploadPurposeDeckAsset:
		// Deck assets authorize against the parent deck.
		return PermissionLibraryView, true
	case UploadPurposeSharedFolder:
		return PermissionLibraryUpload, true
	default:
		return "", false
	}
}
