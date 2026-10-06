package api

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

const connectedDeviceTicketLifetime = 5 * time.Minute

// Same-account devices no longer pair or use server tickets: they trust each
// other through the root-signed device list. The ticket keys remain for Space
// peer sessions between different accounts.

type connectedDeviceTicketClaims struct {
	SpaceID             string   `json:"spaceId,omitempty"`
	AppID               string   `json:"appId,omitempty"`
	InstalledVersion    string   `json:"installedVersion,omitempty"`
	AuthorityGeneration int64    `json:"authorityGeneration,omitempty"`
	Issuer              string   `json:"iss"`
	Audience            string   `json:"aud"`
	JTI                 string   `json:"jti"`
	PairID              string   `json:"pairId"`
	SourceDeviceID      string   `json:"sourceDeviceId"`
	SourceEndpointID    string   `json:"sourceEndpointId"`
	TargetDeviceID      string   `json:"targetDeviceId"`
	TargetEndpointID    string   `json:"targetEndpointId"`
	ProtocolVersion     string   `json:"protocolVersion"`
	Permissions         []string `json:"permissions"`
	IssuedAt            int64    `json:"iat"`
	Expires             int64    `json:"exp"`
}

func (s *AgentsService) SetConnectedDevices(config ConnectedDevicesConfig) {
	s.connectedDevices = config
}

func (s *AgentsService) ConnectedDeviceTicketKeys() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.requireUser(w, r); !ok || !s.requireConnectedDevices(w) {
			return
		}
		keys := map[string]string{}
		for id, key := range s.connectedDevices.PublicKeys {
			keys[id] = encodeConnectedDevicePublicKey(key)
		}
		writeJSON(w, http.StatusOK, map[string]any{"algorithm": "Ed25519", "keys": keys})
	}
}

func (s *AgentsService) requireConnectedDevices(w http.ResponseWriter) bool {
	if !s.connectedDevices.valid() {
		http.Error(w, "connected devices unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func connectedDeviceFingerprint(first, second string) string {
	parts := []string{first, second}
	sort.Strings(parts)
	digest := sha256.Sum256([]byte(parts[0] + "\x00" + parts[1]))
	value := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:5])
	return value[:4] + "-" + value[4:8]
}

func containsClipboardValue(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	return containsForbiddenDeviceValue(value)
}

func containsForbiddenDeviceValue(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			if strings.Contains(normalized, "clipboard") || strings.Contains(normalized, "content") || strings.Contains(normalized, "payload") {
				return true
			}
			if containsForbiddenDeviceValue(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsForbiddenDeviceValue(child) {
				return true
			}
		}
	}
	return false
}

func TestingConnectedDeviceFingerprint(first, second string) string {
	return connectedDeviceFingerprint(first, second)
}

func TestingVerifyConnectedDeviceTicket(ticket string, publicKey ed25519.PublicKey, now time.Time) error {
	parts := strings.Split(ticket, ".")
	if len(parts) != 3 {
		return errors.New("malformed ticket")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || base64.RawURLEncoding.EncodeToString(signature) != parts[2] || !ed25519.Verify(publicKey, []byte(parts[0]+"."+parts[1]), signature) {
		return errors.New("invalid ticket signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return errors.New("malformed ticket")
	}
	var claims connectedDeviceTicketClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Issuer != connectedDeviceTicketIssuer || claims.Audience != connectedDeviceTicketAudience || claims.ProtocolVersion != "misty-device/1" || claims.JTI == "" || now.Unix() >= claims.Expires || claims.Expires-claims.IssuedAt > int64(connectedDeviceTicketLifetime/time.Second) {
		return errors.New("invalid or expired ticket")
	}
	return nil
}
