package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/accounts"
	"github.com/kannachi323/misty/server/internal/platform/security"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

type deviceRequestContextKey struct{}

type deviceRequestIdentity struct {
	UserID   string
	DeviceID string
	State    string
	Key      []byte
}

// DeviceAuthenticated requires the account session AND a fresh signature by the
// unified device key over the exact request, prefixed with its domain string.
// Legacy keys (made in the webview before the unified identity) are refused.
func (s *AgentsService) DeviceAuthenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		deviceID := chi.URLParam(r, "deviceID")
		timestampText := strings.TrimSpace(r.Header.Get("X-Misty-Device-Timestamp"))
		nonce := strings.TrimSpace(r.Header.Get("X-Misty-Device-Nonce"))
		signatureText := strings.TrimSpace(r.Header.Get("X-Misty-Device-Signature"))
		timestamp, err := strconv.ParseInt(timestampText, 10, 64)
		if !deviceIDPattern.MatchString(deviceID) || !deviceNoncePattern.MatchString(nonce) || err != nil || time.Since(time.Unix(timestamp, 0)).Abs() > deviceSignatureMaxSkew {
			http.Error(w, "invalid device authentication", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, deviceSignedBodyLimit))
		if err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		publicKeyText, state, err := s.database.UnifiedDeviceKey(r.Context(), userID, deviceID)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "device_identity_required", "message": "Register this device again to continue."})
			return
		}
		publicKey, keyOK := decodeDevicePublicKey(publicKeyText)
		signature, signatureErr := base64.StdEncoding.DecodeString(signatureText)
		canonical := DeviceSignaturePayload(r.Method, r.URL.EscapedPath(), timestampText, nonce, body)
		if !keyOK || signatureErr != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(publicKey), unifiedDeviceRequestMessage(canonical), signature) {
			http.Error(w, "invalid device authentication", http.StatusUnauthorized)
			return
		}
		if _, err := s.database.ConsumeTrustedDeviceNonce(userID, deviceID, nonce, time.Unix(timestamp, 0).Add(deviceSignatureMaxSkew)); err != nil {
			http.Error(w, "device request already used", http.StatusConflict)
			return
		}
		identity := deviceRequestIdentity{UserID: userID, DeviceID: deviceID, State: state, Key: publicKey}
		next(w, r.WithContext(context.WithValue(r.Context(), deviceRequestContextKey{}, identity)))
	}
}

func requestDevice(r *http.Request) (deviceRequestIdentity, bool) {
	identity, ok := r.Context().Value(deviceRequestContextKey{}).(deviceRequestIdentity)
	return identity, ok
}

// requireAdmittedDevice limits a signed route to devices already added to the
// account with a vault-root grant.
func requireAdmittedDevice(w http.ResponseWriter, r *http.Request) (deviceRequestIdentity, bool) {
	identity, ok := requestDevice(r)
	if !ok || identity.State != "admitted" {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "device_not_added", "message": "Add this device to your account first."})
		return deviceRequestIdentity{}, false
	}
	return identity, true
}

func DeviceSignaturePayload(method, path, timestamp, nonce string, body []byte) string {
	bodyDigest := sha256.Sum256(body)
	return fmt.Sprintf("%s\n%s\n%s\n%s\n%x", strings.ToUpper(method), path, timestamp, nonce, bodyDigest)
}

// RegisterDevice records this install's unified device key as pending. The
// body carries a proof signed by that key, so nobody can register a key they
// do not hold. A pending device can do nothing until it is added.
func (s *AgentsService) RegisterDevice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Name          string          `json:"name"`
			PublicKey     string          `json:"publicKey"`
			Platform      string          `json:"platform"`
			P2PEndpointID string          `json:"p2pEndpointId"`
			OSVersion     string          `json:"osVersion"`
			AppVersion    string          `json:"appVersion"`
			Capabilities  json.RawMessage `json:"capabilities"`
			IssuedAt      int64           `json:"issuedAt"`
			Proof         string          `json:"proof"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		platform := normalizedDevicePlatform(body.Platform)
		if len(body.Capabilities) == 0 {
			body.Capabilities = json.RawMessage(`{}`)
		}
		if !validDeviceName(body.Name) || platform == "" || !validJSONObject(body.Capabilities) || containsLocalPath(body.Capabilities) || containsClipboardValue(body.Capabilities) ||
			!verifyDeviceRegistrationProof(userID, body.PublicKey, body.P2PEndpointID, body.IssuedAt, body.Proof) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		session := ""
		if sid := accounts.SessionID(r); sid != "" {
			session = security.HashToken(sid)
		}
		device, err := s.database.RegisterUnifiedDevice(r.Context(), userID, body.Name, body.PublicKey, body.P2PEndpointID, platform,
			db.CleanDeviceVersion(body.OSVersion), db.CleanDeviceVersion(body.AppVersion), session, body.Capabilities)
		writeAgentResult(w, device, err, http.StatusCreated)
	}
}

// ListDevices returns the account's devices with their signed policies and
// who is online. Addresses are never listed.
func (s *AgentsService) ListDevices() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		devices, err := s.database.AccountDevices(r.Context(), userID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"devices": devices, "presence": s.devices().snapshot(userID)})
	}
}

func (s *AgentsService) HeartbeatDevice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		deviceID := chi.URLParam(r, "deviceID")
		var body struct {
			Capabilities json.RawMessage `json:"capabilities"`
		}
		if !deviceIDPattern.MatchString(deviceID) || decodeAIJSON(w, r, &body) != nil || !validJSONObject(body.Capabilities) || containsLocalPath(body.Capabilities) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		device, err := s.database.HeartbeatTrustedDevice(userID, deviceID, body.Capabilities)
		writeAgentResult(w, device, err, http.StatusOK)
	}
}

func (s *AgentsService) requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, err := sessionUserID(r, s.database)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return "", false
	}
	if userID == "" {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return "", false
	}
	return userID, true
}
