package api

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	envconfig "github.com/misty-org/misty-server/internal/platform/config"
	"github.com/misty-org/misty-server/internal/platform/db"
)

const (
	journalTicketLifetime = 60 * time.Second
	journalTicketIssuer   = "misty-api"
	journalTicketAudience = "misty-journal-collab"
)

var ErrCollaborationUnconfigured = errors.New("journal collaboration is not configured")

type JournalCollabConfig struct {
	Enabled                  bool
	Host                     string
	Issuer                   string
	Audience                 string
	PrivateKey               ed25519.PrivateKey
	controlSecret            []byte
	projectionSecret         []byte
	PreviousProjectionSecret []byte
	roomSalt                 []byte
}

func JournalCollabConfigFromEnv() (JournalCollabConfig, error) {
	config := JournalCollabConfig{
		Enabled:  true,
		Host:     strings.TrimSpace(envconfig.Getenv("PARTYKIT_HOST")),
		Issuer:   journalTicketIssuer,
		Audience: journalTicketAudience,
	}
	if config.Host == "" {
		return JournalCollabConfig{}, errors.New("PARTYKIT_HOST is required: notes are saved through the collaboration Worker")
	}
	if strings.Contains(config.Host, "/") || strings.Contains(config.Host, ":") {
		return JournalCollabConfig{}, errors.New("PARTYKIT_HOST must be a bare hostname")
	}
	privateKey, err := parseEd25519PrivateKey(envconfig.Getenv("JOURNAL_COLLAB_TICKET_PRIVATE_KEY"))
	if err != nil {
		return JournalCollabConfig{}, fmt.Errorf("JOURNAL_COLLAB_TICKET_PRIVATE_KEY: %w", err)
	}
	config.PrivateKey = privateKey
	if config.controlSecret, err = decodeServiceSecret("JOURNAL_COLLAB_CONTROL_SECRET"); err != nil {
		return JournalCollabConfig{}, err
	}
	if config.projectionSecret, err = decodeServiceSecret("JOURNAL_COLLAB_PROJECTION_SECRET"); err != nil {
		return JournalCollabConfig{}, err
	}
	if config.PreviousProjectionSecret, err = decodeOptionalServiceSecret("JOURNAL_COLLAB_PROJECTION_SECRET_PREVIOUS"); err != nil {
		return JournalCollabConfig{}, err
	}
	if config.roomSalt, err = decodeServiceSecret("JOURNAL_COLLAB_ROOM_SALT"); err != nil {
		return JournalCollabConfig{}, err
	}
	return config, nil
}

func (c JournalCollabConfig) httpOrigin() string {
	return "https://" + c.Host
}

func (c JournalCollabConfig) websocketOrigin() string {
	return "wss://" + c.Host
}

func parseEd25519PrivateKey(encoded string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, errors.New("must be base64-encoded PKCS#8")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(raw)
	if err != nil {
		return nil, errors.New("must be a PKCS#8 private key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("must be an Ed25519 key")
	}
	return key, nil
}

func decodeServiceSecret(name string) ([]byte, error) {
	value := strings.TrimSpace(envconfig.Getenv(name))
	if value == "" {
		return nil, fmt.Errorf("%s is required for journal collaboration", name)
	}
	secret, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(secret) < 32 {
		return nil, fmt.Errorf("%s must be at least 32 base64-encoded random bytes", name)
	}
	return secret, nil
}

func decodeOptionalServiceSecret(name string) ([]byte, error) {
	if strings.TrimSpace(envconfig.Getenv(name)) == "" {
		return nil, nil
	}
	return decodeServiceSecret(name)
}

func (c JournalCollabConfig) RoomID(noteID string) string {
	return c.resourceRoomID("note", noteID)
}

func (c JournalCollabConfig) CodeRoomID(sessionID string) string {
	return c.resourceRoomID("code", sessionID)
}

func (c JournalCollabConfig) ItemRoomID(kind, itemID string) string {
	return c.resourceRoomID(kind, itemID)
}

func (c JournalCollabConfig) resourceRoomID(resourceType, resourceID string) string {
	mac := hmac.New(sha256.New, c.roomSalt)
	mac.Write([]byte(resourceType + "-room:" + resourceID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))[:32]
}

func SignServicePayload(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (c JournalCollabConfig) VerifyProjectionSignature(timestamp string, body []byte, signature string) bool {
	if !c.Enabled {
		return false
	}
	expected := SignServicePayload(c.projectionSecret, timestamp, body)
	if hmac.Equal([]byte(expected), []byte(signature)) {
		return true
	}
	if len(c.PreviousProjectionSecret) == 0 {
		return false
	}
	previous := SignServicePayload(c.PreviousProjectionSecret, timestamp, body)
	return hmac.Equal([]byte(previous), []byte(signature))
}

func (c JournalCollabConfig) SignControlRequest(timestamp string, body []byte) string {
	return SignServicePayload(c.controlSecret, timestamp, body)
}

type JournalTicket struct {
	Ticket    string    `json:"ticket"`
	Room      string    `json:"room"`
	URL       string    `json:"url"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

type JournalTicketClaims struct {
	Issuer        string `json:"iss"`
	Audience      string `json:"aud"`
	JTI           string `json:"jti"`
	Subject       string `json:"sub"`
	SpaceID       string `json:"space_id"`
	ResourceType  string `json:"resource_type"`
	ResourceID    string `json:"resource_id"`
	NoteID        string `json:"note_id,omitempty"`
	CodeSessionID string `json:"code_session_id,omitempty"`
	Room          string `json:"room"`
	Role          string `json:"role"`
	ACLVersion    int64  `json:"acl_version"`
	Expires       int64  `json:"exp"`
	GC            *bool  `json:"gc,omitempty"`
}

type collaborationResource struct {
	Type        string
	ID          string
	Party       string
	Generation  string
	KeepDeletes bool
}

func (r collaborationResource) roomKey() string {
	if r.Generation == "" {
		return r.ID
	}
	return r.ID + "#" + r.Generation
}

func (c JournalCollabConfig) MintNoteTicket(userID, spaceID, noteID, role string, aclVersion int64) (JournalTicket, error) {
	return c.mintResourceTicket(
		userID,
		spaceID,
		collaborationResource{Type: "note", ID: noteID, Party: "note-room"},
		role,
		aclVersion,
	)
}

func (c JournalCollabConfig) MintCodeTicket(
	userID, spaceID, sessionID, role string,
	aclVersion int64,
) (JournalTicket, error) {
	return c.mintResourceTicket(
		userID,
		spaceID,
		collaborationResource{Type: "code", ID: sessionID, Party: "code-room"},
		role,
		aclVersion,
	)
}

func AppRoomResourceID(spaceID, appID, roomID string) string {
	return spaceID + ":" + appID + ":" + roomID
}

func FileRoomResourceID(spaceID, itemID, appID, format string) string {
	resourceID := spaceID + ":" + itemID
	if format != "text" {
		resourceID += ":" + appID
	}
	return resourceID
}

func (c JournalCollabConfig) MintAppRoomTicket(
	userID, spaceID, appID, roomID, generation, role string,
	keepDeletes bool,
) (JournalTicket, error) {
	return c.mintResourceTicket(
		userID,
		spaceID,
		collaborationResource{
			Type: "app", ID: AppRoomResourceID(spaceID, appID, roomID), Party: "app-room", Generation: generation,
			KeepDeletes: keepDeletes,
		},
		role,
		1,
	)
}

func (c JournalCollabConfig) MintFileRoomTicket(
	userID, spaceID, itemID, appID, format, role, generation string,
	keepDeletes bool,
) (JournalTicket, error) {
	resource := collaborationResource{
		Type: "file", ID: FileRoomResourceID(spaceID, itemID, appID, format), Party: fileRoomParty,
		KeepDeletes: keepDeletes,
	}
	if format != "text" {
		resource.Generation = generation
	}
	return c.mintResourceTicket(userID, spaceID, resource, role, 1)
}

func (c JournalCollabConfig) MintItemTicket(
	kind db.SpaceItemKind,
	userID, spaceID, itemID, role string,
	aclVersion int64,
) (JournalTicket, error) {
	return c.mintResourceTicket(
		userID,
		spaceID,
		collaborationResource{Type: kind.Name, ID: itemID, Party: kind.Party},
		role,
		aclVersion,
	)
}

func (c JournalCollabConfig) mintResourceTicket(
	userID, spaceID string,
	resource collaborationResource,
	role string,
	aclVersion int64,
) (JournalTicket, error) {
	return c.mintResourceTicketWithLifetime(
		userID, spaceID, resource, collaborationRole(userID, role), aclVersion, journalTicketLifetime,
	)
}
