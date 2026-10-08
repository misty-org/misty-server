package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestClipboardTicketIsSignedForOneAccountRoom(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	config := TestingNewClipboardConfig("clip.example.com", private, []byte("0123456789abcdef0123456789abcdef"))
	now := time.Unix(1_800_000_000, 0)
	ticket, err := config.Mint("user-1", "device-1", now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(ticket.Ticket, ".")
	if len(parts) != 3 {
		t.Fatalf("ticket has %d parts", len(parts))
	}
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	if !ed25519.Verify(public, []byte(parts[0]+"."+parts[1]), signature) {
		t.Fatal("signature does not verify")
	}
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims TestingClipboardTicketClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Audience != "misty-clipboard" || claims.Subject != "user-1" || claims.DeviceID != "device-1" {
		t.Fatalf("unexpected claims %+v", claims)
	}
	if claims.Room != ticket.Room || claims.Room == "user-1" || len(claims.Room) != 64 {
		t.Fatalf("room should be an opaque account digest, got %q", claims.Room)
	}
	if claims.Expires != now.Add(5*time.Minute).Unix() {
		t.Fatalf("unexpected expiry %d", claims.Expires)
	}
	if config.RoomID("user-2") == ticket.Room {
		t.Fatal("accounts must not share a clipboard room")
	}
	if ticket.URL != "https://clip.example.com" {
		t.Fatalf("unexpected url %q", ticket.URL)
	}
}

func TestClipboardTicketNeedsAUserAndDevice(t *testing.T) {
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	config := TestingNewClipboardConfig("clip.example.com", private, []byte("salt-salt-salt-salt-salt-salt-12"))
	if _, err := config.Mint("", "device-1", time.Now()); err == nil {
		t.Fatal("expected an error without a user")
	}
	if _, err := config.Mint("user-1", "", time.Now()); err == nil {
		t.Fatal("expected an error without a device")
	}
}
