package api

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
)

const (
	TestingLibraryUploadTokenHeader    = "X-Misty-Library-Upload-Token"
	TestingLibrarySignedDownloadHeader = "X-Misty-Signed-Download"
	libraryReauthenticationHeader      = "X-Misty-Library-Reauthentication"
	libraryUploadLifetime              = 30 * time.Minute
)

var librarySHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type SpaceLibraryService struct {
	database     *db.Database
	TestingStore LibraryObjectStore
	// egress bounds bytes served per account and across the deployment.
	egress                      *EgressGuard
	TestingUploadsEnabled       bool
	TestingAttachmentsEnabled   bool
	groupsEnabled               bool
	previewsEnabled             bool
	peopleEnabled               bool
	peopleProcessor             LibraryPeopleProcessor
	intelligence                *serveragent.SmartLibraryAnalyzer
	aiEnabled                   bool
	editingEnabled              bool
	mediaProcessor              LibraryMediaProcessor
	metadataExtractor           LibraryMetadataExtractor
	locationsEnabled            bool
	duplicatesEnabled           bool
	importsEnabled              bool
	exportsEnabled              bool
	malwareScanner              LibraryMalwareScanner
	TestingUploadLimits         UploadLimits
	TestingNoteAssetsEnabled    bool
	TestingDrawingAssetsEnabled bool
	TestingTransfers            TransferTTLs
	// presigner is non-nil only when the configured object store can sign R2
	// operations. Local development leaves it nil and keeps the proxy route.
	TestingPresigner     LibraryObjectPresigner
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
	value, err := time.ParseDuration(strings.TrimSpace(envconfig.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// configureTransfers turns on direct transfer whenever the object store can
// sign, which is the only thing that actually determines whether it can work.
//
// Deriving it from capability rather than a flag means production cannot be
// misconfigured into proxying file bytes: the R2 store always signs, so direct
// transfer is always on there. The local and in-memory development stores
// cannot sign, so they transparently keep the proxy route.
func (s *SpaceLibraryService) TestingConfigureTransfers(ttls TransferTTLs) error {
	if _, err := validatePresignTTL(ttls.UploadURLTTL); err != nil {
		return fmt.Errorf("upload URL TTL: %w", err)
	}
	if _, err := validatePresignTTL(ttls.DownloadURLTTL); err != nil {
		return fmt.Errorf("download URL TTL: %w", err)
	}
	s.TestingTransfers = ttls
	if presigner, ok := s.TestingStore.(LibraryObjectPresigner); ok {
		s.TestingPresigner = presigner
	}
	return nil
}

// directTransfersActive reports whether signed URLs are in use. It is purely a
// question of whether the store can sign; there is no off switch.
func (s *SpaceLibraryService) TestingDirectTransfersActive() bool {
	return s.TestingPresigner != nil
}

// UploadPurpose names the kind of upload being performed. Each purpose has its
// own authorization rule and its own maximum file size.
type UploadPurpose = string

const (
	UploadPurposeLibrary        UploadPurpose = db.UploadPurposeLibrary
	UploadPurposeChatAttachment UploadPurpose = db.UploadPurposeChatAttachment
	UploadPurposeNoteAttachment UploadPurpose = db.UploadPurposeNoteAttachment
	UploadPurposeDrawingAsset   UploadPurpose = db.UploadPurposeDrawingAsset
)

// UploadLimits holds the configured maximum file size for each upload purpose.
type UploadLimits struct {
	Library        int64
	ChatAttachment int64
	NoteAttachment int64
	DrawingAsset   int64
}

// DefaultUploadLimits matches the beta product decision: 100 MB Library files,
// 15 MB Journal note/drawing assets, and 10 MB chat attachments.
func DefaultUploadLimits() UploadLimits {
	return UploadLimits{
		Library:        db.DefaultLibraryMaxFileBytes,
		ChatAttachment: db.DefaultChatAttachmentMaxFileBytes,
		NoteAttachment: db.DefaultNoteAttachmentMaxFileBytes,
		DrawingAsset:   db.DefaultDrawingAssetMaxFileBytes,
	}
}

// UploadLimitsFromEnv reads the purpose-specific maximums, falling back to the
// beta defaults. A configured value above the database ceiling is rejected by
// validate() at service construction rather than silently clamped.
func UploadLimitsFromEnv() UploadLimits {
	limits := DefaultUploadLimits()
	limits.Library = positiveEnvBytes("MISTY_LIBRARY_MAX_FILE_BYTES", limits.Library)
	limits.ChatAttachment = positiveEnvBytes("MISTY_CHAT_ATTACHMENT_MAX_FILE_BYTES", limits.ChatAttachment)
	limits.NoteAttachment = positiveEnvBytes("MISTY_NOTE_ATTACHMENT_MAX_FILE_BYTES", limits.NoteAttachment)
	limits.DrawingAsset = positiveEnvBytes("MISTY_DRAWING_ASSET_MAX_FILE_BYTES", limits.DrawingAsset)
	return limits
}

// Max returns the configured maximum for a purpose, or 0 when the purpose is
// unknown. A 0 result must be treated as "reject".
func (l UploadLimits) Max(purpose UploadPurpose) int64 {
	switch purpose {
	case UploadPurposeLibrary:
		return l.Library
	case UploadPurposeChatAttachment:
		return l.ChatAttachment
	case UploadPurposeNoteAttachment:
		return l.NoteAttachment
	case UploadPurposeDrawingAsset:
		return l.DrawingAsset
	default:
		return 0
	}
}

func (l UploadLimits) TestingValidate() error {
	for _, limit := range []struct {
		purpose UploadPurpose
		value   int64
	}{
		{UploadPurposeLibrary, l.Library},
		{UploadPurposeChatAttachment, l.ChatAttachment},
		{UploadPurposeNoteAttachment, l.NoteAttachment},
		{UploadPurposeDrawingAsset, l.DrawingAsset},
	} {
		ceiling := db.MaxUploadBytesForPurpose(limit.purpose)
		if limit.value < 1 || limit.value > ceiling {
			return fmt.Errorf("upload limit for %s must be between 1 and %d bytes", limit.purpose, ceiling)
		}
	}
	return nil
}

func (s *SpaceLibraryService) SetSubsystems(attachmentsEnabled, groupsEnabled, previewsEnabled, peopleEnabled, editingEnabled, locationsEnabled, duplicatesEnabled, importsEnabled, exportsEnabled bool) {
	s.TestingAttachmentsEnabled = attachmentsEnabled
	s.groupsEnabled = groupsEnabled
	s.previewsEnabled = previewsEnabled
	s.peopleEnabled = peopleEnabled
	s.editingEnabled = editingEnabled
	s.locationsEnabled = locationsEnabled
	s.duplicatesEnabled = duplicatesEnabled
	s.importsEnabled = importsEnabled
	s.exportsEnabled = exportsEnabled
}

func (s *SpaceLibraryService) SetMalwareScanner(scanner LibraryMalwareScanner) {
	s.malwareScanner = scanner
}

func (s *SpaceLibraryService) SetIntelligence(analyzer *serveragent.SmartLibraryAnalyzer, aiEnabled bool) {
	s.intelligence = analyzer
	s.aiEnabled = aiEnabled
}

func (s *SpaceLibraryService) SetMetadataExtractor(extractor LibraryMetadataExtractor) {
	s.metadataExtractor = extractor
}

func (s *SpaceLibraryService) TestingUploadPurposeEnabled(purpose UploadPurpose) bool {
	switch purpose {
	case UploadPurposeLibrary:
		return s.TestingUploadsEnabled
	case UploadPurposeChatAttachment:
		return s.TestingAttachmentsEnabled
	case UploadPurposeNoteAttachment:
		// Note assets authorize against the parent note, which only the note
		// routes can check. The generic Library upload endpoint must never
		// accept this purpose.
		return false
	case UploadPurposeDrawingAsset:
		// Drawing assets are bound to a drawing and must use its route.
		return false
	default:
		return false
	}
}

// SetNoteAssetsEnabled turns on the note-asset upload purpose for the note
// routes, which perform the parent-note permission check themselves.
func (s *SpaceLibraryService) SetNoteAssetsEnabled(enabled bool) {
	s.TestingNoteAssetsEnabled = enabled
}

// SetDrawingAssetsEnabled enables image references for Drawing routes.
func (s *SpaceLibraryService) SetDrawingAssetsEnabled(enabled bool) {
	s.TestingDrawingAssetsEnabled = enabled
}
