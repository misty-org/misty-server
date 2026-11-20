package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func testCollabConfig(t *testing.T) JournalCollabConfig {
	t.Helper()
	// The host contract must not depend on whichever development environment
	// invoked the test process.
	t.Setenv("PARTYKIT_HOST", "journal-collab-test.example")
	t.Setenv("MISTY_COLLAB_PUBLIC_URL", "")
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JOURNAL_COLLAB_TICKET_PRIVATE_KEY", base64.StdEncoding.EncodeToString(pkcs8))
	t.Setenv("JOURNAL_COLLAB_CONTROL_SECRET", base64.StdEncoding.EncodeToString(secret))
	t.Setenv("JOURNAL_COLLAB_PROJECTION_SECRET", base64.StdEncoding.EncodeToString(secret))
	t.Setenv("JOURNAL_COLLAB_ROOM_SALT", base64.StdEncoding.EncodeToString(secret))
	config, err := JournalCollabConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func decodeTicketClaims(t *testing.T, ticket string) JournalTicketClaims {
	t.Helper()
	parts := strings.Split(ticket, ".")
	if len(parts) != 3 {
		t.Fatalf("ticket has %d segments, want 3", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims JournalTicketClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

// The signature must verify against the public half, which is all Cloudflare
// ever holds.
func TestMintedTicketVerifiesWithThePublicKeyAlone(t *testing.T) {
	config := testCollabConfig(t)

	ticket, err := config.MintNoteTicket("user_1", "space_1", "note_1", "editor", 4)
	if err != nil {
		t.Fatal(err)
	}

	parts := strings.Split(ticket.Ticket, ".")
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	publicKey := config.PrivateKey.Public().(ed25519.PublicKey)
	if !ed25519.Verify(publicKey, []byte(parts[0]+"."+parts[1]), signature) {
		t.Fatal("minted ticket did not verify against its own public key")
	}

	// Header must pin EdDSA, matching what the Worker requires.
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(header), `"alg":"EdDSA"`) {
		t.Fatalf("header = %s, want alg EdDSA", header)
	}
}

func TestTicketClaimsMatchTheWorkerContract(t *testing.T) {
	config := testCollabConfig(t)

	ticket, err := config.MintNoteTicket("user_1", "space_1", "note_1", "viewer", 7)
	if err != nil {
		t.Fatal(err)
	}
	claims := decodeTicketClaims(t, ticket.Ticket)

	if claims.Issuer != "misty-api" || claims.Audience != "misty-journal-collab" {
		t.Fatalf("issuer/audience = %q/%q", claims.Issuer, claims.Audience)
	}
	if claims.Subject != "user_1" || claims.NoteID != "note_1" ||
		claims.ResourceType != "note" || claims.ResourceID != "note_1" ||
		claims.Role != "viewer" {
		t.Fatalf("claims = %#v", claims)
	}
	if claims.ACLVersion != 7 {
		t.Fatalf("acl_version = %d, want 7", claims.ACLVersion)
	}
	// The room in the claims must be the room in the URL, or the Worker's
	// room-equality check rejects every connection.
	if claims.Room != ticket.Room || !strings.HasSuffix(ticket.URL, "/"+ticket.Room) {
		t.Fatalf("room mismatch: claim %q, ticket %q, url %q", claims.Room, ticket.Room, ticket.URL)
	}
	if claims.JTI == "" {
		t.Fatal("ticket has no jti, so it could be replayed")
	}
}

func TestCodeTicketUsesCodeRoomAndClaims(t *testing.T) {
	config := testCollabConfig(t)

	ticket, err := config.MintCodeTicket("user_1", "space_1", "codesession_1", "editor", 1)
	if err != nil {
		t.Fatal(err)
	}
	claims := decodeTicketClaims(t, ticket.Ticket)
	if claims.ResourceType != "code" ||
		claims.ResourceID != "codesession_1" ||
		claims.CodeSessionID != "codesession_1" ||
		claims.Role != "editor" ||
		claims.NoteID != "" {
		t.Fatalf("code claims = %#v", claims)
	}
	if !strings.Contains(ticket.URL, "/parties/code-room/") || claims.Room != ticket.Room ||
		ticket.Room != config.CodeRoomID("codesession_1") {
		t.Fatalf("code ticket URL = %q room = %q", ticket.URL, ticket.Room)
	}
	if config.CodeRoomID("x_1") == config.RoomID("x_1") {
		t.Fatal("a code session shares a room with another resource of the same id")
	}
}

// 60 seconds: long enough to open a socket, short enough that a
// revoked user cannot sit on a usable ticket.
func TestTicketExpiresInOneMinute(t *testing.T) {
	config := testCollabConfig(t)

	ticket, err := config.MintNoteTicket("user_1", "space_1", "note_1", "editor", 1)
	if err != nil {
		t.Fatal(err)
	}
	claims := decodeTicketClaims(t, ticket.Ticket)

	lifetime := time.Unix(claims.Expires, 0).Sub(time.Now())
	if lifetime > 65*time.Second || lifetime < 55*time.Second {
		t.Fatalf("ticket lifetime = %s, want about 60s", lifetime)
	}
}

func TestExportTicketIsViewerOnlySingleUseAndShortLived(t *testing.T) {
	config := testCollabConfig(t)
	ticket, err := config.MintJournalExportTicket(
		"user_1", "space_1", "note", "note_1", 8,
	)
	if err != nil {
		t.Fatal(err)
	}
	claims := decodeTicketClaims(t, ticket.Ticket)
	if claims.Role != "viewer" || claims.ResourceType != "note" ||
		claims.ResourceID != "note_1" || claims.NoteID != "note_1" || claims.ACLVersion != 8 ||
		claims.JTI == "" {
		t.Fatalf("export claims = %#v", claims)
	}
	lifetime := time.Until(ticket.ExpiresAt)
	if lifetime < 14*time.Minute || lifetime > 16*time.Minute {
		t.Fatalf("export ticket lifetime = %s, want about 15 minutes", lifetime)
	}
}

// Every connection needs a distinct jti or the room's single-use check would
// reject the second one.
func TestEveryTicketGetsAFreshJTI(t *testing.T) {
	config := testCollabConfig(t)
	seen := map[string]bool{}

	for range 25 {
		ticket, err := config.MintNoteTicket("user_1", "space_1", "note_1", "editor", 1)
		if err != nil {
			t.Fatal(err)
		}
		jti := decodeTicketClaims(t, ticket.Ticket).JTI
		if seen[jti] {
			t.Fatalf("jti %q was reused", jti)
		}
		seen[jti] = true
	}
}

// The room appears in a URL handled by a third-party edge network, so it must
// not be the note id.
func TestRoomIDIsOpaqueButStable(t *testing.T) {
	config := testCollabConfig(t)

	room := config.RoomID("note_1")
	if room == "note_1" || strings.Contains(room, "note_1") {
		t.Fatalf("room %q leaks the note id", room)
	}
	if config.RoomID("note_1") != room {
		t.Fatal("RoomID is not deterministic, so control commands would miss the room")
	}
	if config.RoomID("note_2") == room {
		t.Fatal("two notes share a room")
	}
	if len(room) != 32 {
		t.Fatalf("room length = %d, want 32", len(room))
	}
}

func TestJournalCollabConfigUsesTheOperatorsHost(t *testing.T) {
	config := testCollabConfig(t)

	if !config.Enabled || config.Host != "journal-collab-test.example" {
		t.Fatalf("host = %q enabled = %v, want the configured host", config.Host, config.Enabled)
	}
}

func TestJournalCollabIsRequired(t *testing.T) {
	testCollabConfig(t)
	// Every server saves notes through the Worker.
	t.Setenv("PARTYKIT_HOST", "")
	if _, err := JournalCollabConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "PARTYKIT_HOST") {
		t.Fatalf("no host = %v; want a startup error", err)
	}
	if _, err := (JournalCollabConfig{}).MintNoteTicket("user", "space", "note", "editor", 1); !errors.Is(err, ErrCollaborationUnconfigured) {
		t.Fatalf("ticket from an unconfigured config = %v", err)
	}
	// A host without its keys still fails startup.
	t.Setenv("PARTYKIT_HOST", "journal-collab-test.example")
	t.Setenv("JOURNAL_COLLAB_ROOM_SALT", "")
	if _, err := JournalCollabConfigFromEnv(); err == nil {
		t.Fatal("a host without its keys was accepted")
	}
}

// Misty's own Space apps are gone: their kinds mint no tickets, and the registry's one kind still
// reaches its own room.
func TestItemTicketsFollowTheRegistry(t *testing.T) {
	config := testCollabConfig(t)
	note, ok := db.SpaceItemKindNamed("note")
	if !ok {
		t.Fatal("note is not a registered kind")
	}
	ticket, err := config.MintItemTicket(note, "user_1", "space_1", "note_1", "editor", 3)
	if err != nil {
		t.Fatal(err)
	}
	claims := decodeTicketClaims(t, ticket.Ticket)
	if claims.ResourceType != "note" || claims.NoteID != "note_1" || claims.Room != config.RoomID("note_1") ||
		!strings.Contains(ticket.URL, "/parties/note-room/") || config.ItemRoomID("note", "note_1") != config.RoomID("note_1") {
		t.Fatalf("note item ticket = %#v %q", claims, ticket.URL)
	}
	for _, removed := range []string{"drawing", "image", "deck", "workbook"} {
		if _, err := config.MintJournalExportTicket("user_1", "space_1", removed, removed+"_1", 1); err == nil {
			t.Fatalf("a %s export ticket was minted", removed)
		}
	}
	if _, err := config.MintJournalExportTicket("user_1", "space_1", "notes", "note_1", 1); err == nil {
		t.Fatal("a section name minted an export ticket")
	}
}
