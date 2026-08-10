package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	PermissionStorageViewMembers = "storage.view_member_usage"
	PermissionStorageManage      = "storage.manage"
	PermissionStorageViewOwn     = "storage.view_own_usage"
	PermissionStudioView         = "studio.view"
	PermissionStudioManage       = "studio.manage"
	PermissionAgentsRun          = "agents.run"
	PermissionAgentsManage       = "agents.manage"
	PermissionTasksView          = "tasks.view"
	PermissionTasksManage        = "tasks.manage"
	PermissionIntegrationsManage = "integrations.manage"
	PermissionSpaceDelete        = "space.delete"
	LibraryRecoveryWindow        = 30 * 24 * time.Hour
)

// Upload purposes decide which Space permission an upload needs and which
// maximum file size applies. The database enforces the maximum independently of
// the API service so a misconfigured service cannot widen the limit.
const (
	UploadPurposeLibrary        = "library"
	UploadPurposeChatAttachment = "attachment"
	UploadPurposeNoteAttachment = "note_attachment"
	UploadPurposeDrawingAsset   = "drawing_attachment"
)

// Default per-purpose maximums. Deployments may lower these through
// configuration, but never above the ceiling enforced here.
const (
	DefaultLibraryMaxFileBytes        = int64(100 << 20)
	DefaultChatAttachmentMaxFileBytes = int64(10 << 20)
	DefaultNoteAttachmentMaxFileBytes = int64(15 << 20)
	DefaultDrawingAssetMaxFileBytes   = int64(15 << 20)
)

// MaxUploadBytesForPurpose is the hard database ceiling for a purpose. An
// unknown purpose has no valid ceiling and returns 0.
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
	PermissionStorageManage, PermissionStudioView, PermissionStudioManage, PermissionAgentsRun,
	PermissionAgentsManage,
	PermissionTasksView, PermissionTasksManage, PermissionIntegrationsManage,
}

var (
	ErrLibraryNotFound         = errors.New("library resource not found")
	ErrLibraryForbidden        = errors.New("library permission denied")
	ErrLibraryInvalid          = errors.New("invalid library request")
	ErrLibraryQuota            = errors.New("space storage quota exceeded")
	ErrLibraryConflict         = errors.New("library resource version conflict")
	ErrLibraryReauthentication = errors.New("library reauthentication required")
	ErrLibraryUploadMismatch   = errors.New("library upload does not match its reservation")
)

type SpaceStorageUsage struct {
	SpaceID            string `json:"space_id"`
	OwnerUserID        string `json:"owner_user_id,omitempty"`
	SpaceUsedBytes     int64  `json:"space_used_bytes"`
	SpaceReservedBytes int64  `json:"space_reserved_bytes"`
	UsedBytes          int64  `json:"used_bytes"`
	ReservedBytes      int64  `json:"reserved_bytes"`
	LimitBytes         int64  `json:"limit_bytes"`
	RemainingBytes     int64  `json:"remaining_bytes"`
	Version            int64  `json:"version"`
}

type LibraryUpload struct {
	ID                     string    `json:"id"`
	SpaceID                string    `json:"space_id"`
	SecurityDomainID       string    `json:"security_domain_id"`
	UserID                 string    `json:"user_id"`
	ObjectKey              string    `json:"-"`
	OriginalFilename       string    `json:"original_filename"`
	Purpose                string    `json:"purpose"`
	ClientDeclaredMIMEType string    `json:"client_declared_mime_type"`
	RequestedByteSize      int64     `json:"requested_byte_size"`
	ClientSHA256           string    `json:"client_sha256"`
	VerifiedByteSize       *int64    `json:"verified_byte_size,omitempty"`
	VerifiedSHA256         string    `json:"verified_sha256,omitempty"`
	DetectedMIMEType       string    `json:"detected_mime_type,omitempty"`
	State                  string    `json:"state"`
	FileID                 string    `json:"file_id,omitempty"`
	UploadTokenHash        string    `json:"-"`
	ErrorCode              string    `json:"error_code,omitempty"`
	ExpiresAt              time.Time `json:"expires_at"`
	Version                int64     `json:"version"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type LibraryFile struct {
	ID                 string          `json:"id"`
	BlobID             string          `json:"blob_id"`
	SecurityDomainID   string          `json:"security_domain_id"`
	UploaderUserID     string          `json:"uploader_user_id"`
	OriginalFilename   string          `json:"original_filename"`
	IntrinsicMetadata  json.RawMessage `json:"intrinsic_metadata"`
	LifecycleState     string          `json:"lifecycle_state"`
	OriginalUploadedAt time.Time       `json:"original_uploaded_at"`
	Version            int64           `json:"version"`
}

type SpaceLibraryItem struct {
	ID                     string          `json:"id"`
	SpaceID                string          `json:"space_id"`
	FileID                 string          `json:"file_id"`
	ContributingUserID     string          `json:"contributing_user_id"`
	DisplayName            string          `json:"display_name"`
	Caption                string          `json:"caption"`
	Tags                   []string        `json:"tags"`
	Favorite               bool            `json:"favorite"`
	Hidden                 bool            `json:"hidden"`
	DateOverride           *time.Time      `json:"date_override,omitempty"`
	LocationOverride       json.RawMessage `json:"location_override,omitempty"`
	ContributorInformation json.RawMessage `json:"contributor_information"`
	CurrentEditVersionID   string          `json:"current_edit_version_id,omitempty"`
	AddedByUserID          string          `json:"added_by_user_id"`
	LifecycleState         string          `json:"lifecycle_state"`
	AddedAt                time.Time       `json:"added_at"`
	TrashedAt              *time.Time      `json:"trashed_at,omitempty"`
	RecoverUntil           *time.Time      `json:"recover_until,omitempty"`
	Version                int64           `json:"version"`
	UpdatedAt              time.Time       `json:"updated_at"`
	File                   LibraryFile     `json:"file"`
}

type MessageAttachment struct {
	ID             string     `json:"id"`
	SpaceID        string     `json:"space_id"`
	MessageID      string     `json:"message_id,omitempty"`
	FileID         string     `json:"file_id"`
	UploadID       string     `json:"upload_id"`
	UploaderUserID string     `json:"uploader_user_id"`
	DisplayName    string     `json:"display_name"`
	PromotedItemID string     `json:"promoted_item_id,omitempty"`
	LifecycleState string     `json:"lifecycle_state"`
	CreatedAt      time.Time  `json:"created_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	RecoverUntil   *time.Time `json:"recover_until,omitempty"`
}

type CompleteLibraryUploadResult struct {
	Upload           LibraryUpload      `json:"upload"`
	File             LibraryFile        `json:"file"`
	Item             *SpaceLibraryItem  `json:"item,omitempty"`
	Attachment       *MessageAttachment `json:"attachment,omitempty"`
	NoteAsset        *SpaceNoteAsset    `json:"note_asset,omitempty"`
	DrawingAsset     *SpaceDrawingAsset `json:"drawing_asset,omitempty"`
	DiscardObjectKey string             `json:"-"`
}

type LibraryDownload struct {
	ObjectKey string
	Filename  string
	MIMEType  string
	ByteSize  int64
	SHA256    string
	Rendition bool
}

type ExpiredLibraryUpload struct {
	ID        string
	ObjectKey string
}

func (db *Database) HasSpacePermission(ctx context.Context, userID, spaceID, permission string) (bool, error) {
	allowed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var err error
		allowed, err = hasSpacePermissionTx(ctx, tx, userID, spaceID, permission)
		return err
	})
	return allowed, err
}
