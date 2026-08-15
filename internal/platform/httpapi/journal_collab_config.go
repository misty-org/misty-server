package api

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

// Journal collaboration tickets for notes and drawings.
//
// A ticket is the only thing the Cloudflare Worker trusts. It is signed with an
// Ed25519 private key that never leaves this server, so a compromise of the
// collaboration service cannot mint access to a private note.

const (
	// Short enough that a revoked user cannot hold a usable ticket for long,
	// long enough to survive a slow client opening a WebSocket.
	journalTicketLifetime    = 60 * time.Second
	journalTicketIssuer      = "misty-api"
	journalTicketAudience    = "misty-journal-collab"
	defaultJournalCollabHost = "misty-journal-collab.mistysys.workers.dev"
)

// JournalCollabConfig holds everything needed to talk to the shared note and
// drawing collaboration service. It is only usable once every field validates.
type JournalCollabConfig struct {
	Host                            string
	PublicOrigin                    string
	Issuer                          string
	Audience                        string
	TestingPrivateKey               ed25519.PrivateKey
	controlSecret                   []byte
	projectionSecret                []byte
	TestingPreviousProjectionSecret []byte
	roomSalt                        []byte
}

// JournalCollabConfigFromEnv reads the collaboration configuration.
//
// Journal collaboration is an invariant, not a feature flag. A missing or
// invalid setting fails startup rather than quietly degrading to local-only
// notes.
func JournalCollabConfigFromEnv() (JournalCollabConfig, error) {
	config := JournalCollabConfig{
		Host:     envOrDefault("PARTYKIT_HOST", defaultJournalCollabHost),
		Issuer:   journalTicketIssuer,
		Audience: journalTicketAudience,
	}
	selfHosted := strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_DEPLOYMENT_MODE")), "self_hosted")
	if selfHosted && strings.TrimSpace(envconfig.Getenv("MISTY_COLLAB_PUBLIC_URL")) != "" {
		origin, err := validateCollaborationPublicOrigin(envconfig.Getenv("MISTY_COLLAB_PUBLIC_URL"))
		if err != nil {
			return JournalCollabConfig{}, err
		}
		config.PublicOrigin = origin
		config.Host = strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	}
	if config.Host == "" {
		return JournalCollabConfig{}, errors.New("PARTYKIT_HOST is required for journal collaboration")
	}
	if config.PublicOrigin == "" && (strings.Contains(config.Host, "/") || strings.Contains(config.Host, ":")) {
		return JournalCollabConfig{}, errors.New("PARTYKIT_HOST must be a bare hostname")
	}
	privateKey, err := parseEd25519PrivateKey(envconfig.Getenv("JOURNAL_COLLAB_TICKET_PRIVATE_KEY"))
	if err != nil {
		return JournalCollabConfig{}, fmt.Errorf("JOURNAL_COLLAB_TICKET_PRIVATE_KEY: %w", err)
	}
	config.TestingPrivateKey = privateKey
	if config.controlSecret, err = decodeServiceSecret("JOURNAL_COLLAB_CONTROL_SECRET"); err != nil {
		return JournalCollabConfig{}, err
	}
	if config.projectionSecret, err = decodeServiceSecret("JOURNAL_COLLAB_PROJECTION_SECRET"); err != nil {
		return JournalCollabConfig{}, err
	}
	if config.TestingPreviousProjectionSecret, err = decodeOptionalServiceSecret("JOURNAL_COLLAB_PROJECTION_SECRET_PREVIOUS"); err != nil {
		return JournalCollabConfig{}, err
	}
	if config.roomSalt, err = decodeServiceSecret("JOURNAL_COLLAB_ROOM_SALT"); err != nil {
		return JournalCollabConfig{}, err
	}
	return config, nil
}

func validateCollaborationPublicOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("MISTY_COLLAB_PUBLIC_URL must be an origin URL")
	}
	loopback := parsed.Hostname() == "localhost" || net.ParseIP(parsed.Hostname()) != nil && net.ParseIP(parsed.Hostname()).IsLoopback()
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
		return "", errors.New("MISTY_COLLAB_PUBLIC_URL must use HTTPS except on loopback")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (c JournalCollabConfig) httpOrigin() string {
	if c.PublicOrigin != "" {
		return c.PublicOrigin
	}
	return "https://" + c.Host
}

func (c JournalCollabConfig) websocketOrigin() string {
	origin := c.httpOrigin()
	if strings.HasPrefix(origin, "http://") {
		return "ws://" + strings.TrimPrefix(origin, "http://")
	}
	return "wss://" + strings.TrimPrefix(origin, "https://")
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(envconfig.Getenv(name)); value != "" {
		return value
	}
	return fallback
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

// RoomID derives the opaque room identifier for a note.
//
// The room appears in the collaboration WebSocket URL, which is handled by a
// third-party edge network, so it is deliberately not the note id. The
// derivation is deterministic, so both the ticket endpoint and the control
// sender reach the same room without storing an extra column.
func (c JournalCollabConfig) RoomID(noteID string) string {
	return c.resourceRoomID("note", noteID)
}

// DrawingRoomID derives the opaque room identifier for a drawing.
func (c JournalCollabConfig) DrawingRoomID(drawingID string) string {
	return c.resourceRoomID("drawing", drawingID)
}

func (c JournalCollabConfig) resourceRoomID(resourceType, resourceID string) string {
	mac := hmac.New(sha256.New, c.roomSalt)
	mac.Write([]byte(resourceType + "-room:" + resourceID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))[:32]
}

// SignServicePayload produces the HMAC a service-to-service call carries.
// Timestamp and body are both covered so a captured call cannot be replayed
// later or have its body swapped.
func TestingSignServicePayload(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyProjectionSignature checks a projection callback from the Worker.
func (c JournalCollabConfig) VerifyProjectionSignature(timestamp string, body []byte, signature string) bool {
	expected := TestingSignServicePayload(c.projectionSecret, timestamp, body)
	if hmac.Equal([]byte(expected), []byte(signature)) {
		return true
	}
	if len(c.TestingPreviousProjectionSecret) == 0 {
		return false
	}
	previous := TestingSignServicePayload(c.TestingPreviousProjectionSecret, timestamp, body)
	return hmac.Equal([]byte(previous), []byte(signature))
}

// SignControlRequest signs a control command sent to the Worker.
func (c JournalCollabConfig) SignControlRequest(timestamp string, body []byte) string {
	return TestingSignServicePayload(c.controlSecret, timestamp, body)
}

// JournalTicket is what the desktop needs in order to open a collaboration socket.
type JournalTicket struct {
	Ticket    string    `json:"ticket"`
	Room      string    `json:"room"`
	URL       string    `json:"url"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

type TestingJournalTicketClaims struct {
	Issuer       string `json:"iss"`
	Audience     string `json:"aud"`
	JTI          string `json:"jti"`
	Subject      string `json:"sub"`
	SpaceID      string `json:"space_id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	NoteID       string `json:"note_id,omitempty"`
	DrawingID    string `json:"drawing_id,omitempty"`
	Room         string `json:"room"`
	Role         string `json:"role"`
	ACLVersion   int64  `json:"acl_version"`
	Expires      int64  `json:"exp"`
}

type collaborationResource struct {
	Type  string
	ID    string
	Party string
}

// MintNoteTicket signs a single-connection ticket for a note.
//
// Callers must have rechecked note access immediately before calling this: the
// ticket is a bearer credential for the room, and nothing downstream re-reads
// the ACL.
func (c JournalCollabConfig) MintNoteTicket(userID, spaceID, noteID, role string, aclVersion int64) (JournalTicket, error) {
	return c.mintResourceTicket(
		userID,
		spaceID,
		collaborationResource{Type: "note", ID: noteID, Party: "note-room"},
		role,
		aclVersion,
	)
}

// MintDrawingTicket signs a single-use ticket for one DrawingRoom connection.
func (c JournalCollabConfig) MintDrawingTicket(
	userID, spaceID, drawingID, role string,
	aclVersion int64,
) (JournalTicket, error) {
	return c.mintResourceTicket(
		userID,
		spaceID,
		collaborationResource{Type: "drawing", ID: drawingID, Party: "drawing-room"},
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
		userID, spaceID, resource, role, aclVersion, journalTicketLifetime,
	)
}
