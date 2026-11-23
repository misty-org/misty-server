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
	database             *db.Database
	Store                LibraryObjectStore
	egress               *EgressGuard
	AttachmentsEnabled   bool
	UploadLimits         UploadLimits
	NoteAssetsEnabled    bool
	Transfers            TransferTTLs
	Presigner            LibraryObjectPresigner
	reconciliationMu     sync.Mutex
	reconciliationCursor string
}

type TransferTTLs struct {
	UploadURLTTL   time.Duration
	DownloadURLTTL time.Duration
}

func DefaultTransferTTLs() TransferTTLs {
	return TransferTTLs{UploadURLTTL: 15 * time.Minute, DownloadURLTTL: 2 * time.Minute}
}

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

var ErrLibraryDirectTransferUnavailable = errors.New("library direct transfer unavailable")

func (s *SpaceLibraryService) DirectTransfersActive() bool {
	return s.Presigner != nil
}

type UploadPurpose = string

const (
	UploadPurposeChatAttachment UploadPurpose = db.UploadPurposeChatAttachment
	UploadPurposeNoteAttachment UploadPurpose = db.UploadPurposeNoteAttachment
	UploadPurposeDrive          UploadPurpose = db.UploadPurposeDrive
)

type UploadLimits struct {
	ChatAttachment int64
	NoteAttachment int64
}

func DefaultUploadLimits() UploadLimits {
	return UploadLimits{
		ChatAttachment: db.DefaultChatAttachmentMaxFileBytes,
		NoteAttachment: db.DefaultNoteAttachmentMaxFileBytes,
	}
}

func UploadLimitsFromEnv() UploadLimits {
	limits := DefaultUploadLimits()
	limits.ChatAttachment = positiveEnvBytes("MISTY_CHAT_ATTACHMENT_MAX_FILE_BYTES", limits.ChatAttachment)
	limits.NoteAttachment = positiveEnvBytes("MISTY_NOTE_ATTACHMENT_MAX_FILE_BYTES", limits.NoteAttachment)
	return limits
}

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

func (s *SpaceLibraryService) SetAttachmentsEnabled(enabled bool) {
	s.AttachmentsEnabled = enabled
}

func (s *SpaceLibraryService) UploadPurposeEnabled(purpose UploadPurpose) bool {
	switch purpose {
	case UploadPurposeChatAttachment:
		return s.AttachmentsEnabled
	case UploadPurposeNoteAttachment:
		return false
	case UploadPurposeDrive:
		return false
	default:
		return false
	}
}

func (s *SpaceLibraryService) SetNoteAssetsEnabled(enabled bool) {
	s.NoteAssetsEnabled = enabled
}
