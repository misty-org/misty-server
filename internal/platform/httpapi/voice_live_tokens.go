package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// liveTicketDomain separates live transcription tickets from every other use
// of the runtime control secret. The runtime verifies the same string.
const liveTicketDomain = "misty-live-ticket\n"

// LiveTicketClaims bind one live transcription session to an account, a
// device, its channels and its audio limit. The runtime verifies the ticket
// statelessly and settles against SessionID exactly once.
type LiveTicketClaims struct {
	SessionID string   `json:"sid"`
	User      string   `json:"sub"`
	Device    string   `json:"dev"`
	Channels  []string `json:"ch"`
	MaxMS     int64    `json:"max"`
	Language  string   `json:"lang,omitempty"`
	Model     string   `json:"model"`
	ExpiresAt int64    `json:"exp"`
}

var errLiveTicket = errors.New("invalid live ticket")

func signLiveTicket(secret []byte, claims LiveTicketClaims) (string, error) {
	if len(secret) == 0 {
		return "", errLiveTicket
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return "v1." + encoded + "." + liveTicketMAC(secret, encoded), nil
}

func liveTicketMAC(secret []byte, encoded string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(liveTicketDomain + encoded))
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyLiveTicket mirrors the runtime's check; the server uses it in tests
// and to read back tickets it issued.
func verifyLiveTicket(secrets [][]byte, ticket string, now time.Time) (LiveTicketClaims, error) {
	var claims LiveTicketClaims
	version, rest, ok := strings.Cut(ticket, ".")
	encoded, signature, ok2 := strings.Cut(rest, ".")
	if !ok || !ok2 || version != "v1" || len(ticket) > 4096 {
		return claims, errLiveTicket
	}
	valid := false
	for _, secret := range secrets {
		if len(secret) > 0 && hmac.Equal([]byte(liveTicketMAC(secret, encoded)), []byte(signature)) {
			valid = true
		}
	}
	if !valid {
		return claims, errLiveTicket
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || json.Unmarshal(payload, &claims) != nil {
		return claims, errLiveTicket
	}
	if claims.SessionID == "" || claims.User == "" || claims.MaxMS <= 0 || now.Unix() >= claims.ExpiresAt {
		return claims, errLiveTicket
	}
	return claims, nil
}

// liveChannels accepts "mic" and "system", each once.
func liveChannels(requested []string) ([]string, bool) {
	seen := map[string]bool{}
	out := []string{}
	for _, channel := range requested {
		if (channel != "mic" && channel != "system") || seen[channel] {
			return nil, false
		}
		seen[channel] = true
		out = append(out, channel)
	}
	return out, len(out) > 0
}
