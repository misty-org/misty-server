package api

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/misty-org/misty-server/internal/platform/config"

	"github.com/misty-org/misty-server/internal/platform/db"
)

const (
	LibraryUploadTokenHeader    = "X-Misty-Library-Upload-Token"
	LibrarySignedDownloadHeader = "X-Misty-Signed-Download"
	libraryUploadLifetime       = 30 * time.Minute
)

var librarySHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type SpaceLibraryService struct {
	database *db.Database
	Store    LibraryObjectStore
	// egress bounds bytes served per account and across the deployment.
	egress             *EgressGuard
	AttachmentsEnabled bool
	UploadLimits       UploadLimits
	NoteAssetsEnabled  bool
	Transfers          TransferTTLs
	// presigner is non-nil only when the configured object store can sign R2
	// operations. Without one (a development server without R2), transfers
	// are refused: file bytes never pass through the server.
	Presigner            LibraryObjectPresigner
	reconciliationMu     sync.Mutex
	reconciliationCursor string
}

// TransferTTLs bounds how long a signed R2 URL stays valid. These are tuning
// knobs, not a feature switch: there is deliberately no way to turn direct
// transfer off, because routing user file bytes through the VPS is never the
// behaviour we want in production.
type TransferTTLs struct {
	UploadURLTTL   time.Duration
	DownloadURLTTL time.Duration
}

// DefaultTransferTTLs matches the documented beta configuration.
func DefaultTransferTTLs() TransferTTLs {
	return TransferTTLs{UploadURLTTL: 15 * time.Minute, DownloadURLTTL: 2 * time.Minute}
}

// TransferTTLsFromEnv allows tuning the two URL lifetimes.
func TransferTTLsFromEnv() TransferTTLs {
	ttls := DefaultTransferTTLs()
	ttls.UploadURLTTL = positiveEnvDuration("MISTY_R2_UPLOAD_URL_TTL", ttls.UploadURLTTL)
	ttls.DownloadURLTTL = positiveEnvDuration("MISTY_R2_DOWNLOAD_URL_TTL", ttls.DownloadURLTTL)
	return ttls
}

func positiveEnvDuration(name string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(config.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// ConfigureTransfers turns on direct transfer whenever the object store can
// sign. Deriving it from capability rather than a flag means production cannot
// be misconfigured into proxying file bytes; the local and in-memory
// development stores cannot sign, so they keep the proxy route.
func (s *SpaceLibraryService) ConfigureTransfers(ttls TransferTTLs) error {
	if _, err := validatePresignTTL(ttls.UploadURLTTL); err != nil {
		return fmt.Errorf("upload URL TTL: %w", err)
	}
	if _, err := validatePresignTTL(ttls.DownloadURLTTL); err != nil {
		return fmt.Errorf("download URL TTL: %w", err)
	}
	s.Transfers = ttls
	if presigner, ok := s.Store.(LibraryObjectPresigner); ok {
		s.Presigner = presigner
	}
	return nil
}

// ErrLibraryDirectTransferUnavailable refuses a transfer when the store can't
// sign R2 URLs: file bytes go straight between the app and R2, never through
// the server, so there is nothing else to fall back to.
var ErrLibraryDirectTransferUnavailable = errors.New("library direct transfer unavailable")

// directTransfersActive reports whether signed URLs are in use. It is purely a
// question of whether the store can sign; there is no off switch.
func (s *SpaceLibraryService) DirectTransfersActive() bool {
	return s.Presigner != nil
}

// UploadPurpose names the kind of upload being performed. Each purpose has its
// own authorization rule and its own maximum file size.
type UploadPurpose = string

const (
	UploadPurposeChatAttachment UploadPurpose = db.UploadPurposeChatAttachment
	UploadPurposeNoteAttachment UploadPurpose = db.UploadPurposeNoteAttachment
	UploadPurposeDrive          UploadPurpose = db.UploadPurposeDrive
)

// UploadLimits holds the configured maximum file size for each upload purpose.
type UploadLimits struct {
	ChatAttachment int64
	NoteAttachment int64
}

// DefaultUploadLimits matches the beta product decision: 15 MB note assets
// and 10 MB chat attachments.
func DefaultUploadLimits() UploadLimits {
	return UploadLimits{
		ChatAttachment: db.DefaultChatAttachmentMaxFileBytes,
		NoteAttachment: db.DefaultNoteAttachmentMaxFileBytes,
	}
}

// UploadLimitsFromEnv reads the purpose-specific maximums, falling back to the
// beta defaults. A configured value above the database ceiling is rejected by
// validate() at service construction rather than silently clamped.
func UploadLimitsFromEnv() UploadLimits {
	limits := DefaultUploadLimits()
	limits.ChatAttachment = positiveEnvBytes("MISTY_CHAT_ATTACHMENT_MAX_FILE_BYTES", limits.ChatAttachment)
	limits.NoteAttachment = positiveEnvBytes("MISTY_NOTE_ATTACHMENT_MAX_FILE_BYTES", limits.NoteAttachment)
	return limits
}

// Max returns the configured maximum for a purpose, or 0 when the purpose is
// unknown. A 0 result must be treated as "reject".
func (l UploadLimits) Max(purpose UploadPurpose) int64 {
	switch purpose {
	case UploadPurposeChatAttachment:
		return l.ChatAttachment
	case UploadPurposeNoteAttachment:
		return l.NoteAttachment
	default:
		return 0
	}
}

func (l UploadLimits) Validate() error {
	for _, limit := range []struct {
		purpose UploadPurpose
		value   int64
	}{
		{UploadPurposeChatAttachment, l.ChatAttachment},
		{UploadPurposeNoteAttachment, l.NoteAttachment},
	} {
		ceiling := db.MaxUploadBytesForPurpose(limit.purpose)
		if limit.value < 1 || limit.value > ceiling {
			return fmt.Errorf("upload limit for %s must be between 1 and %d bytes", limit.purpose, ceiling)
		}
	}
	return nil
}

// SetAttachmentsEnabled turns on chat attachment uploads.
func (s *SpaceLibraryService) SetAttachmentsEnabled(enabled bool) {
	s.AttachmentsEnabled = enabled
}

func (s *SpaceLibraryService) UploadPurposeEnabled(purpose UploadPurpose) bool {
	switch purpose {
	case UploadPurposeChatAttachment:
		return s.AttachmentsEnabled
	case UploadPurposeNoteAttachment:
		// Note assets authorize against the parent note, which only the note
		// routes can check. The generic upload endpoint must never
		// accept this purpose.
		return false
	case UploadPurposeDrive:
		// Drive files are started by the drive's upload route, never here.
		return false
	default:
		return false
	}
}

// SetNoteAssetsEnabled turns on the note-asset upload purpose for the note
// routes, which perform the parent-note permission check themselves.
func (s *SpaceLibraryService) SetNoteAssetsEnabled(enabled bool) {
	s.NoteAssetsEnabled = enabled
}
