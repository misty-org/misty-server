package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kannachi323/misty/server/internal/accounts"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"github.com/kannachi323/misty/server/internal/platform/security"
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
		canonical := TestingDeviceSignaturePayload(r.Method, r.URL.EscapedPath(), timestampText, nonce, body)
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

func TestingDeviceSignaturePayload(method, path, timestamp, nonce string, body []byte) string {
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

func validDeviceName(name string) bool {
	return validText(name, 1, 64) && !strings.ContainsFunc(name, func(value rune) bool { return value < 0x20 || value == 0x7f })
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
	var invalidRequest serveragent.ErrInvalidRequest
	switch {
	case errors.Is(err, billingadapter.ErrDenied), errors.Is(err, billingadapter.ErrUnavailable):
		writeBillingError(w, err)
	case errors.Is(err, db.ErrDeviceIdentityConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "device_identity_conflict", "message": "This device endpoint is already registered with a different signing key. Restore the original device identity or set up a new device identity before trying again."})
	case errors.Is(err, db.ErrAgentModelTurnLimit):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "agent_model_turn_limit", "message": "This run reached its model-turn limit. Review its completed work before starting another request."})
	case errors.Is(err, db.ErrAgentExecutionTimeLimit):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "agent_execution_time_limit", "message": "This run used its execution-time allowance. Review its completed work before starting another request."})
	case errors.As(err, &invalidRequest):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "invalid_tool_input", "message": invalidRequest.Error()})
	case errors.Is(err, db.ErrLibraryForbidden), errors.Is(err, db.ErrSpaceForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied", "message": "Your current Space access does not allow that action."})
	case errors.Is(err, db.ErrDeviceNotFound), errors.Is(err, db.ErrAgentJobNotFound), errors.Is(err, db.ErrAgentNotFound), errors.Is(err, db.ErrPersonalAgentNotFound), errors.Is(err, db.ErrSpaceNotFound), errors.Is(err, db.ErrLibraryNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "The selected item is no longer available in this Space."})
	case errors.Is(err, db.ErrDeviceKeyRevoked):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "device_removed", "message": "This device was removed from your account."})
	case errors.Is(err, db.ErrDeviceNotAdmitted):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "device_not_added", "message": "Add this device to your account first."})
	case errors.Is(err, db.ErrDeviceListConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "device_list_conflict", "message": "Your devices changed. Misty will try again."})
	case errors.Is(err, db.ErrDeviceVaultMissing):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "sync_setup_required", "message": "Set up sync to add devices."})
	case errors.Is(err, db.ErrDeviceAdmissionState):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "admission_state", "message": "This approval ended. Start again from the new device."})
	case errors.Is(err, db.ErrDevicePolicyStale):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "device_policy_stale", "message": "A newer setting already applies."})
	case errors.Is(err, db.ErrPersonalAgentConflict), errors.Is(err, db.ErrLibraryConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "version_conflict", "message": "That item changed while the Agent was working. Please retry."})
	case errors.Is(err, db.ErrSpaceConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "run_conflict", "message": "The requested change conflicts with newer Space data. Please retry."})
	case errors.Is(err, db.ErrSpaceInvalid), errors.Is(err, db.ErrLibraryInvalid):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "invalid_tool_input", "message": "The requested values are not valid for this action."})
	case errors.Is(err, db.ErrPersonalStorageQuota):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "storage_limit_reached", "reason": "personal_storage_limit_reached", "message": "Your account has reached its cloud storage limit."})
	case errors.Is(err, db.ErrSpaceStorageQuota):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "storage_limit_reached", "reason": "personal_storage_limit_reached", "message": "Your account has reached its cloud storage limit."})
	case errors.Is(err, db.ErrLibraryQuota):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "storage_limit_reached", "message": "Storage quota has been reached."})
	case errors.Is(err, db.ErrLibraryReauthentication):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "reauthentication_required", "message": "This Library item requires you to confirm access first."})
	case errors.Is(err, db.ErrPersonalAgentModel):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "agent_model_unavailable"})
	case isHostedAILimitReached(err):
		scope, _ := hostedAILimitScope(err)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "hosted_ai_limit_reached", "reason": hostedAILimitReason(scope), "message": hostedAILimitMessage(scope)})
	case errors.Is(err, db.ErrInvalidLease), errors.Is(err, db.ErrInvalidJobState):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "invalid_or_expired_lease"})
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func isHostedAILimitReached(err error) bool {
	_, ok := hostedAILimitScope(err)
	return ok
}

func hostedAILimitScope(err error) (string, bool) {
	var agentLimit serveragent.HostedAILimitReachedError
	if errors.As(err, &agentLimit) {
		return agentLimit.Scope, true
	}
	var databaseLimit db.HostedAILimitReachedError
	if errors.As(err, &databaseLimit) {
		return databaseLimit.Scope, true
	}
	return "", false
}

func hostedAILimitReason(_ string) string { return "personal_ai_limit_reached" }

func hostedAILimitMessage(_ string) string { return "Your account has used its available AI budget." }

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
	for _, allowed := range []string{"macos", "windows", "linux", "unknown"} {
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
