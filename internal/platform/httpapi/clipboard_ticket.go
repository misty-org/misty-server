package api

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

// The cloud clipboard (docs/design/clipboard/BRIEF.md) runs on a Cloudflare
// Worker. Clips are encrypted on the device with a key derived from the vault
// root, so this server only decides who may connect: it signs a short ticket
// for an admitted device whose own signed policy turns the clipboard on.

const (
	clipboardTicketLifetime = 5 * time.Minute
	clipboardTicketIssuer   = "misty-api"
	clipboardTicketAudience = "misty-clipboard"
)

// ClipboardConfig holds the ticket key and the Worker's host.
type ClipboardConfig struct {
	Host       string
	privateKey ed25519.PrivateKey
	roomSalt   []byte
}

// ClipboardConfigFromEnv reads MISTY_CLIPBOARD_HOST, CLIPBOARD_TICKET_PRIVATE_KEY
// and CLIPBOARD_ROOM_SALT. The clipboard is off when the host is unset.
func ClipboardConfigFromEnv() (ClipboardConfig, error) {
	host := strings.TrimSpace(envconfig.Getenv("MISTY_CLIPBOARD_HOST"))
	if host == "" {
		return ClipboardConfig{}, errors.New("MISTY_CLIPBOARD_HOST is not set")
	}
	if strings.ContainsAny(host, "/:?#@ ") {
		return ClipboardConfig{}, errors.New("MISTY_CLIPBOARD_HOST must be a bare hostname")
	}
	key, err := parseEd25519PrivateKey(envconfig.Getenv("CLIPBOARD_TICKET_PRIVATE_KEY"))
	if err != nil {
		return ClipboardConfig{}, fmt.Errorf("CLIPBOARD_TICKET_PRIVATE_KEY: %w", err)
	}
	salt, err := base64.StdEncoding.DecodeString(strings.TrimSpace(envconfig.Getenv("CLIPBOARD_ROOM_SALT")))
	if err != nil || len(salt) < 32 {
		return ClipboardConfig{}, errors.New("CLIPBOARD_ROOM_SALT must be at least 32 base64-encoded random bytes")
	}
	return ClipboardConfig{Host: host, privateKey: key, roomSalt: salt}, nil
}

// TestingNewClipboardConfig builds a config for tests.
func TestingNewClipboardConfig(host string, key ed25519.PrivateKey, salt []byte) ClipboardConfig {
	return ClipboardConfig{Host: host, privateKey: key, roomSalt: salt}
}

// ClipboardTicket is what a device needs to reach its account's clipboard.
type ClipboardTicket struct {
	Ticket    string    `json:"ticket"`
	Room      string    `json:"room"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// TestingClipboardTicketClaims are the signed claims the Worker checks.
type TestingClipboardTicketClaims struct {
	Issuer   string `json:"iss"`
	Audience string `json:"aud"`
	JTI      string `json:"jti"`
	Subject  string `json:"sub"`
	DeviceID string `json:"device_id"`
	Room     string `json:"room"`
	Expires  int64  `json:"exp"`
}

// RoomID names the account's clipboard room without revealing the account id
// to Cloudflare.
func (c ClipboardConfig) RoomID(userID string) string {
	mac := hmac.New(sha256.New, c.roomSalt)
	mac.Write([]byte("misty.clipboard.room.v1\n" + userID))
	return hex.EncodeToString(mac.Sum(nil))
}

// Mint signs a ticket for one device on one account.
func (c ClipboardConfig) Mint(userID, deviceID string, now time.Time) (ClipboardTicket, error) {
	if userID == "" || deviceID == "" || len(c.privateKey) != ed25519.PrivateKeySize {
		return ClipboardTicket{}, errors.New("incomplete clipboard ticket")
	}
	expiresAt := now.Add(clipboardTicketLifetime).UTC()
	room := c.RoomID(userID)
	claims := TestingClipboardTicketClaims{
		Issuer: clipboardTicketIssuer, Audience: clipboardTicketAudience,
		JTI: "clip_" + uuid.NewString(), Subject: userID, DeviceID: deviceID,
		Room: room, Expires: expiresAt.Unix(),
	}
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT"})
	if err != nil {
		return ClipboardTicket{}, err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return ClipboardTicket{}, err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	signature := ed25519.Sign(c.privateKey, []byte(signingInput))
	return ClipboardTicket{
		Ticket:    signingInput + "." + base64.RawURLEncoding.EncodeToString(signature),
		Room:      room,
		URL:       "https://" + c.Host,
		ExpiresAt: expiresAt,
	}, nil
}

// ClipboardTicket issues a ticket to an admitted device whose signed policy has
// the clipboard on. Mount it behind DeviceAuthenticated.
func (s *AgentsService) ClipboardTicket(config ClipboardConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		identity, ok := requireAdmittedDevice(w, r)
		if !ok {
			return
		}
		enabled, err := s.database.DeviceClipboardEnabled(r.Context(), identity.UserID, identity.DeviceID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		if !enabled {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "clipboard_off", "message": "Turn on Clipboard for this device in Settings."})
			return
		}
		ticket, err := config.Mint(identity.UserID, identity.DeviceID, time.Now())
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, ticket)
	}
}
