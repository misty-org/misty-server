package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

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
		publicKeyText, err := s.database.TrustedDevicePublicKey(userID, deviceID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		publicKey, keyErr := decodeDeviceBase64(publicKeyText)
		signature, signatureErr := decodeDeviceBase64(signatureText)
		canonical := TestingDeviceSignaturePayload(r.Method, r.URL.EscapedPath(), timestampText, nonce, body)
		if keyErr != nil || signatureErr != nil || len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(publicKey), []byte(canonical), signature) {
			http.Error(w, "invalid device authentication", http.StatusUnauthorized)
			return
		}
		if _, err := s.database.ConsumeTrustedDeviceNonce(userID, deviceID, nonce, time.Unix(timestamp, 0).Add(deviceSignatureMaxSkew)); err != nil {
			http.Error(w, "device request already used", http.StatusConflict)
			return
		}
		next(w, r)
	}
}

func TestingDeviceSignaturePayload(method, path, timestamp, nonce string, body []byte) string {
	bodyDigest := sha256.Sum256(body)
	return fmt.Sprintf("%s\n%s\n%s\n%s\n%x", strings.ToUpper(method), path, timestamp, nonce, bodyDigest)
}

func (s *AgentsService) RegisterDevice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Name             string          `json:"name"`
			PublicKey        string          `json:"publicKey"`
			KeyAlgorithm     string          `json:"keyAlgorithm"`
			Capabilities     json.RawMessage `json:"capabilities"`
			Platform         string          `json:"platform"`
			P2PEndpointID    string          `json:"p2pEndpointId"`
			ProtocolVersions json.RawMessage `json:"protocolVersions"`
		}
		if decodeAIJSON(w, r, &body) != nil || !TestingValidDeviceRegistrationV2(body.Name, body.PublicKey, body.KeyAlgorithm, body.Platform, body.P2PEndpointID, body.ProtocolVersions, body.Capabilities) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		device, err := s.database.RegisterTrustedDevice(userID, strings.TrimSpace(body.Name), strings.TrimSpace(body.PublicKey), normalizedDevicePlatform(body.Platform), strings.TrimSpace(body.P2PEndpointID), normalizedProtocolVersions(body.ProtocolVersions), body.Capabilities)
		writeAgentResult(w, device, err, http.StatusCreated)
	}
}

func (s *AgentsService) ListDevices() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		devices, err := s.database.TrustedDevices(userID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
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

func (s *AgentsService) RevokeDevice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		deviceID := chi.URLParam(r, "deviceID")
		if !deviceIDPattern.MatchString(deviceID) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := s.database.RevokeTrustedDevice(userID, deviceID); err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
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

func writeAgentResult(w http.ResponseWriter, value any, err error, status int) {
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, status, value)
}

func writeAgentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrLibraryForbidden), errors.Is(err, db.ErrSpaceForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
	case errors.Is(err, db.ErrDeviceNotFound), errors.Is(err, db.ErrAgentJobNotFound), errors.Is(err, db.ErrAgentNotFound), errors.Is(err, db.ErrPersonalAgentNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
	case errors.Is(err, db.ErrPairingNotFound), errors.Is(err, db.ErrDevicePair):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "pairing_not_found"})
	case errors.Is(err, db.ErrPairingExpired):
		writeJSON(w, http.StatusGone, map[string]string{"code": "pairing_expired"})
	case errors.Is(err, db.ErrPairingLocked):
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "pairing_locked"})
	case errors.Is(err, db.ErrPairingState):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "invalid_pairing_state"})
	case errors.Is(err, db.ErrPersonalAgentConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "version_conflict"})
	case errors.Is(err, db.ErrSpaceConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "run_conflict"})
	case errors.Is(err, db.ErrSpaceInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
	case errors.Is(err, db.ErrPersonalAgentModel):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "agent_model_unavailable"})
	case isHostedAILimitReached(err):
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "hosted_ai_limit_reached"})
	case errors.Is(err, db.ErrInvalidLease), errors.Is(err, db.ErrInvalidJobState):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "invalid_or_expired_lease"})
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func isHostedAILimitReached(err error) bool {
	var exhausted serveragent.HostedAILimitReachedError
	return errors.As(err, &exhausted)
}

func validText(value string, min, max int) bool {
	length := utf8.RuneCountInString(strings.TrimSpace(value))
	return length >= min && length <= max
}

func validJSONObject(raw json.RawMessage) bool {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return false
	}
	_, ok := value.(map[string]any)
	return ok
}

func TestingValidDeviceRegistration(name, key, algorithm string, capabilities json.RawMessage) bool {
	return TestingValidDeviceRegistrationV2(name, key, algorithm, "", "", nil, capabilities)
}

func TestingValidDeviceRegistrationV2(name, key, algorithm, platform, endpointID string, protocolVersions, capabilities json.RawMessage) bool {
	decodedKey, err := decodeDeviceBase64(key)
	if !validText(name, 1, 100) || err != nil || len(decodedKey) != ed25519.PublicKeySize || (algorithm != "" && algorithm != "ed25519") || !validJSONObject(capabilities) || containsLocalPath(capabilities) || containsClipboardValue(capabilities) {
		return false
	}
	platform = normalizedDevicePlatform(platform)
	if platform == "" || (endpointID != "" && !p2pEndpointIDPattern.MatchString(endpointID)) {
		return false
	}
	if len(protocolVersions) == 0 {
		return endpointID == ""
	}
	var versions []string
	if json.Unmarshal(protocolVersions, &versions) != nil || len(versions) > 8 {
		return false
	}
	for _, version := range versions {
		if version != "misty-device/1" {
			return false
		}
	}
	return (endpointID == "") == (len(versions) == 0)
}

func normalizedDevicePlatform(platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		return "unknown"
	}
	for _, allowed := range []string{"macos", "windows", "linux", "ios", "android", "unknown"} {
		if platform == allowed {
			return platform
		}
	}
	return ""
}

func normalizedProtocolVersions(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`[]`)
	}
	return raw
}

func decodeDeviceBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("invalid base64")
}

func containsLocalPath(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	return containsPathValue(value)
}

func containsPathValue(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			if normalized == "path" || strings.HasSuffix(normalized, "path") {
				return true
			}
			if containsPathValue(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsPathValue(child) {
				return true
			}
		}
	}
	return false
}
