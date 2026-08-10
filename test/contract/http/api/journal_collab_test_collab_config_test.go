package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func testCollabConfig(t *testing.T) JournalCollabConfig {
	t.Helper()
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

func decodeTicketClaims(t *testing.T, ticket string) TestingJournalTicketClaims {
	t.Helper()
	parts := strings.Split(ticket, ".")
	if len(parts) != 3 {
		t.Fatalf("ticket has %d segments, want 3", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims TestingJournalTicketClaims
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
	publicKey := config.TestingPrivateKey.Public().(ed25519.PublicKey)
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

func TestDrawingTicketUsesDrawingRoomAndClaims(t *testing.T) {
	config := testCollabConfig(t)

	ticket, err := config.MintDrawingTicket(
		"user_1", "space_1", "drawing_1", "editor", 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	claims := decodeTicketClaims(t, ticket.Ticket)
	if claims.ResourceType != "drawing" ||
		claims.ResourceID != "drawing_1" ||
		claims.DrawingID != "drawing_1" ||
		claims.NoteID != "" {
		t.Fatalf("drawing claims = %#v", claims)
	}
	if !strings.Contains(ticket.URL, "/parties/drawing-room/") {
		t.Fatalf("drawing ticket URL = %q", ticket.URL)
	}
	if config.DrawingRoomID("drawing_1") == config.RoomID("drawing_1") {
		t.Fatal("a note and drawing with the same id share a room")
	}
}

// 60 seconds, per the plan: long enough to open a socket, short enough that a
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
		"user_1", "space_1", "drawing", "drawing_1", 8,
	)
	if err != nil {
		t.Fatal(err)
	}
	claims := decodeTicketClaims(t, ticket.Ticket)
	if claims.Role != "viewer" || claims.ResourceType != "drawing" ||
		claims.ResourceID != "drawing_1" || claims.ACLVersion != 8 ||
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

func TestJournalCollabConfigUsesMistyWorkerHost(t *testing.T) {
	config := testCollabConfig(t)

	if config.Host != "misty-journal-collab.mistysys.workers.dev" {
		t.Fatalf("host = %q, want Misty worker host", config.Host)
	}
}
