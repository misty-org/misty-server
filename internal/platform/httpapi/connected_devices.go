package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const connectedDeviceTicketLifetime = 5 * time.Minute

type connectedDeviceTicketClaims struct {
	Issuer           string   `json:"iss"`
	Audience         string   `json:"aud"`
	JTI              string   `json:"jti"`
	PairID           string   `json:"pairId"`
	SourceDeviceID   string   `json:"sourceDeviceId"`
	SourceEndpointID string   `json:"sourceEndpointId"`
	TargetDeviceID   string   `json:"targetDeviceId"`
	TargetEndpointID string   `json:"targetEndpointId"`
	ProtocolVersion  string   `json:"protocolVersion"`
	Permissions      []string `json:"permissions"`
	IssuedAt         int64    `json:"iat"`
	Expires          int64    `json:"exp"`
}

func (s *AgentsService) SetConnectedDevices(config ConnectedDevicesConfig) {
	s.connectedDevices = config
}

func (s *AgentsService) CreateDevicePairing() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok || !s.requireConnectedDevices(w) {
			return
		}
		deviceID := chi.URLParam(r, "deviceID")
		secret, err := db.TestingSecureToken()
		if err != nil {
			writeAgentError(w, err)
			return
		}
		code, err := secureManualPairingCode()
		if err != nil {
			writeAgentError(w, err)
			return
		}
		expiresAt := time.Now().UTC().Add(5 * time.Minute)
		session, err := s.database.CreateDevicePairingSession(userID, deviceID, s.connectedDevices.hashPairingSecret(secret), s.connectedDevices.hashPairingSecret(code), expiresAt)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"session":    session,
			"manualCode": code,
			"deepLink":   fmt.Sprintf("misty://devices/pair?session=%s&secret=%s", session.ID, secret),
		})
	}
}

func (s *AgentsService) RedeemDevicePairing() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok || !s.requireConnectedDevices(w) {
			return
		}
		deviceID := chi.URLParam(r, "deviceID")
		var body struct {
			SessionID string `json:"sessionId"`
			Secret    string `json:"secret"`
			Code      string `json:"code"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		credential := strings.TrimSpace(body.Secret)
		if credential == "" {
			credential = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(body.Code), "-", ""))
		}
		if credential == "" || (body.SessionID != "" && !pairingSessionIDPattern.MatchString(body.SessionID)) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		session, err := s.database.RedeemDevicePairingSession(userID, deviceID, body.SessionID, s.connectedDevices.hashPairingSecret(credential))
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"session": session, "fingerprint": connectedDeviceFingerprint(session.CreatorEndpointID, pointerValue(session.RequesterEndpointID))})
	}
}

func (s *AgentsService) DevicePairingStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok || !s.requireConnectedDevices(w) {
			return
		}
		session, err := s.database.DevicePairingSession(userID, chi.URLParam(r, "deviceID"), chi.URLParam(r, "sessionID"))
		if err != nil {
			writeAgentError(w, err)
			return
		}
		response := map[string]any{"session": session}
		if session.RequesterEndpointID != nil {
			response["fingerprint"] = connectedDeviceFingerprint(session.CreatorEndpointID, *session.RequesterEndpointID)
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func (s *AgentsService) ConfirmDevicePairing() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok || !s.requireConnectedDevices(w) {
			return
		}
		pair, err := s.database.ConfirmDevicePairing(userID, chi.URLParam(r, "deviceID"), chi.URLParam(r, "sessionID"))
		writeAgentResult(w, pair, err, http.StatusOK)
	}
}

func (s *AgentsService) UpdateConnectedDevicePresence() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok || !s.requireConnectedDevices(w) {
			return
		}
		var body struct {
			EndpointID      string          `json:"endpointId"`
			ProtocolVersion string          `json:"protocolVersion"`
			ConnectionHint  string          `json:"connectionHint"`
			Addressing      json.RawMessage `json:"addressing"`
		}
		if decodeAIJSON(w, r, &body) != nil || !p2pEndpointIDPattern.MatchString(body.EndpointID) || body.ProtocolVersion != "misty-device/1" || (body.ConnectionHint != "unknown" && body.ConnectionHint != "direct" && body.ConnectionHint != "relay") || !validJSONObject(body.Addressing) || containsLocalPath(body.Addressing) || containsClipboardValue(body.Addressing) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		err := s.database.UpdateDevicePresence(userID, chi.URLParam(r, "deviceID"), body.EndpointID, body.ProtocolVersion, body.ConnectionHint, body.Addressing)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "online"})
	}
}

func (s *AgentsService) ListConnectedDevicePeers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok || !s.requireConnectedDevices(w) {
			return
		}
		peers, err := s.database.ConnectedPeers(userID, chi.URLParam(r, "deviceID"))
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"peers": peers, "onlineWindowSeconds": 90})
	}
}

func (s *AgentsService) ConnectedDeviceClipboardConsent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok || !s.requireConnectedDevices(w) {
			return
		}
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if decodeAIJSON(w, r, &body) != nil || body.Enabled == nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		err := s.database.SetDevicePairClipboardConsent(userID, chi.URLParam(r, "deviceID"), chi.URLParam(r, "pairID"), *body.Enabled)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"enabled": *body.Enabled})
	}
}

func (s *AgentsService) RenameConnectedDevicePeer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok || !s.requireConnectedDevices(w) {
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		if body.Name == "" || len(body.Name) > 80 || strings.ContainsFunc(body.Name, func(value rune) bool { return value < 0x20 || value == 0x7f }) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		err := s.database.SetDevicePairPeerName(userID, chi.URLParam(r, "deviceID"), chi.URLParam(r, "pairID"), body.Name)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": body.Name})
	}
}

func (s *AgentsService) RevokeConnectedDevicePair() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok || !s.requireConnectedDevices(w) {
			return
		}
		if err := s.database.RevokeDevicePair(userID, chi.URLParam(r, "deviceID"), chi.URLParam(r, "pairID")); err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
	}
}

func (s *AgentsService) IssueConnectedDeviceTicket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok || !s.requireConnectedDevices(w) {
			return
		}
		var body struct {
			TargetDeviceID  string `json:"targetDeviceId"`
			ProtocolVersion string `json:"protocolVersion"`
		}
		if decodeAIJSON(w, r, &body) != nil || !deviceIDPattern.MatchString(body.TargetDeviceID) || body.ProtocolVersion != "misty-device/1" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		subject, err := s.database.PeerTicketSubject(userID, chi.URLParam(r, "deviceID"), body.TargetDeviceID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		permissions := []string{"roots:read", "files:read", "directories:subscribe"}
		if subject.ClipboardSourceToTarget {
			permissions = append(permissions, "clipboard:send")
		}
		if subject.ClipboardTargetToSource {
			permissions = append(permissions, "clipboard:receive")
		}
		now := time.Now().UTC()
		claims := connectedDeviceTicketClaims{
			Issuer: connectedDeviceTicketIssuer, Audience: connectedDeviceTicketAudience, JTI: "peerticket_" + uuid.NewString(),
			PairID: subject.PairID, SourceDeviceID: subject.SourceDeviceID, SourceEndpointID: subject.SourceEndpointID,
			TargetDeviceID: subject.TargetDeviceID, TargetEndpointID: subject.TargetEndpointID,
			ProtocolVersion: body.ProtocolVersion, Permissions: permissions, IssuedAt: now.Unix(), Expires: now.Add(connectedDeviceTicketLifetime).Unix(),
		}
		ticket, err := s.signConnectedDeviceTicket(claims)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ticket": ticket, "keyId": s.connectedDevices.KeyID, "expiresAt": time.Unix(claims.Expires, 0).UTC()})
	}
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

func (s *AgentsService) signConnectedDeviceTicket(claims connectedDeviceTicketClaims) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": s.connectedDevices.KeyID})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature := ed25519.Sign(s.connectedDevices.PrivateKey, []byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *AgentsService) requireConnectedDevices(w http.ResponseWriter) bool {
	if !s.connectedDevices.valid() {
		http.Error(w, "connected devices unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func secureManualPairingCode() (string, error) {
	raw := make([]byte, 5)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

func connectedDeviceFingerprint(first, second string) string {
	parts := []string{first, second}
	sort.Strings(parts)
	digest := sha256.Sum256([]byte(parts[0] + "\x00" + parts[1]))
	value := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:5])
	return value[:4] + "-" + value[4:8]
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
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
