package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/accounts"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// Devices: admission, signed list, policies, names and removal
// (docs/design/devices/BRIEF.md). Every change that affects trust carries a
// signature the server checks but cannot produce.

// DeviceTrust returns what clients verify against: the vault root public key
// and the current root-signed device list.
func (s *AgentsService) DeviceTrust() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		response := map[string]any{"accountId": userID, "vault": nil, "list": nil}
		root, err := s.database.DeviceVaultRoot(r.Context(), userID)
		if err != nil && !errors.Is(err, db.ErrDeviceVaultMissing) {
			writeAgentError(w, err)
			return
		}
		if root != nil {
			response["vault"] = map[string]any{"vaultId": root.VaultID, "rootPublicKey": base64.StdEncoding.EncodeToString(root.RootPublicKey), "keyEpoch": root.KeyEpoch}
		}
		list, err := s.database.CurrentDeviceList(r.Context(), userID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		if list != nil {
			response["list"] = map[string]any{"payload": base64.StdEncoding.EncodeToString(list.Payload), "signature": list.Signature, "version": list.Version}
		}
		writeJSON(w, http.StatusOK, response)
	}
}

// admissionInputs verifies a grant and the list that adds its device against
// the account's vault root.
func (s *AgentsService) admissionInputs(r *http.Request, userID string, grantRecord, listRecord signedDeviceRecord) (*admissionGrant, *db.DeviceListChange, error) {
	root, err := s.database.DeviceVaultRoot(r.Context(), userID)
	if err != nil {
		return nil, nil, err
	}
	grant, err := parseDeviceGrant(grantRecord, root.RootPublicKey)
	if err != nil || grant.AccountID != userID || grant.VaultID != root.VaultID || grant.KeyEpoch != root.KeyEpoch {
		return nil, nil, errSignedDeviceRecord
	}
	change, accountID, epoch, err := parseDeviceList(listRecord, root.RootPublicKey)
	if err != nil || accountID != userID || change.VaultID != root.VaultID || epoch != root.KeyEpoch {
		return nil, nil, errSignedDeviceRecord
	}
	return grant, change, nil
}

// AdmitDevice is Path A: the device derived the vault root from the sync
// password and secret (or a key it saved) and signed its own grant.
func (s *AgentsService) AdmitDevice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, ok := requestDevice(r)
		if !ok {
			return
		}
		var body struct {
			Grant signedDeviceRecord `json:"grant"`
			List  signedDeviceRecord `json:"list"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		grant, change, err := s.admissionInputs(r, device.UserID, body.Grant, body.List)
		if errors.Is(err, db.ErrDeviceVaultMissing) {
			writeAgentError(w, err)
			return
		}
		if err != nil || grant.DeviceID != device.DeviceID || grant.ApprovedByDeviceID != "" || grant.PublicKey != base64.StdEncoding.EncodeToString(device.Key) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_device_grant", "message": "This device's grant could not be verified."})
			return
		}
		session := ""
		if sid := accounts.SessionID(r); sid != "" {
			session = security.HashToken(sid)
		}
		if err := s.database.AdmitDevice(r.Context(), device.UserID, session, grant.record(), *change); err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "admitted", "listVersion": change.Version})
	}
}

// CreateAdmissionRequest is Path B, step 1: a pending device asks an added one
// to approve it, committing to a nonce it reveals only after the challenge.
func (s *AgentsService) CreateAdmissionRequest() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, ok := requestDevice(r)
		if !ok {
			return
		}
		if device.State != "pending" {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "admission_state", "message": "This device is already added."})
			return
		}
		var body struct {
			Request signedDeviceRecord `json:"request"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		request, err := parseDeviceAdmissionRequest(body.Request, device.Key)
		if err != nil || request.AccountID != device.UserID || request.DeviceID != device.DeviceID || request.PublicKey != base64.StdEncoding.EncodeToString(device.Key) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		created, err := s.database.CreateDeviceAdmissionRequest(r.Context(), device.UserID, device.DeviceID, request.Payload, body.Request.Signature)
		writeAgentResult(w, created, err, http.StatusCreated)
	}
}

// ListAdmissionRequests shows open approvals to the account's devices.
func (s *AgentsService) ListAdmissionRequests() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		requests, err := s.database.OpenDeviceAdmissionRequests(r.Context(), userID)
		writeAgentResult(w, map[string]any{"requests": requests}, err, http.StatusOK)
	}
}

// AdmissionRequest lets the requester and approver follow one approval.
func (s *AgentsService) AdmissionRequest() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, ok := requestDevice(r)
		if !ok {
			return
		}
		request, err := s.database.DeviceAdmissionRequest(r.Context(), device.UserID, chi.URLParam(r, "requestID"))
		if err != nil {
			writeAgentError(w, err)
			return
		}
		if request.DeviceID != device.DeviceID && request.ApproverDeviceID != device.DeviceID && device.State != "admitted" {
			writeAgentError(w, db.ErrDeviceNotFound)
			return
		}
		writeJSON(w, http.StatusOK, request)
	}
}

// ChallengeAdmission is step 2: an added device sends its ephemeral key and a
// nonce. Each request takes one challenge.
func (s *AgentsService) ChallengeAdmission() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, ok := requireAdmittedDevice(w, r)
		if !ok {
			return
		}
		var body struct {
			X25519Public string `json:"x25519Public"`
			Nonce        string `json:"nonce"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		if !validX25519Public(body.X25519Public) || !validDeviceNonce(body.Nonce) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		err := s.database.ChallengeDeviceAdmission(r.Context(), device.UserID, chi.URLParam(r, "requestID"), device.DeviceID, body.X25519Public, body.Nonce)
		writeAgentResult(w, map[string]string{"status": "challenged"}, err, http.StatusOK)
	}
}

// RevealAdmission is step 3: the new device reveals its committed nonce. The
// server checks the commitment; the approver checks it again.
func (s *AgentsService) RevealAdmission() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, ok := requestDevice(r)
		if !ok {
			return
		}
		var body struct {
			Nonce string `json:"nonce"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		request, err := s.database.DeviceAdmissionRequest(r.Context(), device.UserID, chi.URLParam(r, "requestID"))
		if err != nil {
			writeAgentError(w, err)
			return
		}
		parsed, parseErr := parseStoredAdmissionRequest(request, device.Key)
		raw, decodeErr := base64.StdEncoding.DecodeString(body.Nonce)
		digest := sha256.Sum256(raw)
		if request.DeviceID != device.DeviceID || parseErr != nil || !validDeviceNonce(body.Nonce) || decodeErr != nil || hex.EncodeToString(digest[:]) != parsed.Commitment {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		err = s.database.RevealDeviceAdmission(r.Context(), device.UserID, request.ID, device.DeviceID, body.Nonce)
		writeAgentResult(w, map[string]string{"status": "revealed"}, err, http.StatusOK)
	}
}

// ApproveAdmission is step 4: after the person confirmed matching codes, the
// approver sends the grant, the new list and the vault key sealed to the new
// device's key. The server stores the sealed bytes; it cannot open them.
func (s *AgentsService) ApproveAdmission() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, ok := requireAdmittedDevice(w, r)
		if !ok {
			return
		}
		var body struct {
			Grant      signedDeviceRecord `json:"grant"`
			List       signedDeviceRecord `json:"list"`
			SealedRoot string             `json:"sealedRoot"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		sealed, sealedErr := base64.StdEncoding.DecodeString(body.SealedRoot)
		grant, change, err := s.admissionInputs(r, device.UserID, body.Grant, body.List)
		if err != nil || sealedErr != nil || len(sealed) < 60 || len(sealed) > 512 || grant.ApprovedByDeviceID != device.DeviceID {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_device_grant", "message": "The approval could not be verified."})
			return
		}
		target, err := s.database.AccountDevice(r.Context(), device.UserID, grant.DeviceID)
		if err != nil || target.PublicKey != grant.PublicKey {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_device_grant", "message": "The approval names a different device."})
			return
		}
		if err := s.database.ApproveDeviceAdmission(r.Context(), device.UserID, chi.URLParam(r, "requestID"), device.DeviceID, body.SealedRoot, grant.record(), *change); err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "approved", "listVersion": change.Version})
	}
}

func (s *AgentsService) DenyAdmission() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, ok := requestDevice(r)
		if !ok {
			return
		}
		err := s.database.DenyDeviceAdmission(r.Context(), device.UserID, chi.URLParam(r, "requestID"), device.DeviceID)
		writeAgentResult(w, map[string]string{"status": "denied"}, err, http.StatusOK)
	}
}

// RenameDevice sets one device's name. Any added device may rename any device;
// a pending device may rename only itself.
func (s *AgentsService) RenameDevice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, ok := requestDevice(r)
		if !ok {
			return
		}
		target := chi.URLParam(r, "targetID")
		var body struct {
			Name string `json:"name"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		if !deviceIDPattern.MatchString(target) || !validDeviceName(body.Name) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if target != device.DeviceID && device.State != "admitted" {
			writeAgentError(w, db.ErrDeviceNotAdmitted)
			return
		}
		err := s.database.RenameDevice(r.Context(), device.UserID, target, body.Name)
		writeAgentResult(w, map[string]string{"name": body.Name}, err, http.StatusOK)
	}
}

// StoreDevicePolicy records a device's own signed permissions. Only the device
// itself can sign them, so permissions change only on that device.
func (s *AgentsService) StoreDevicePolicy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, ok := requestDevice(r)
		if !ok {
			return
		}
		var body struct {
			Policy signedDeviceRecord `json:"policy"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		policy, err := parseDevicePolicy(body.Policy, device.Key)
		if err != nil || policy.AccountID != device.UserID || policy.DeviceID != device.DeviceID {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		err = s.database.StoreDevicePolicy(r.Context(), device.UserID, device.DeviceID, policy.Record)
		writeAgentResult(w, map[string]any{"version": policy.Record.Version}, err, http.StatusOK)
	}
}

// RemoveDevice removes a device from the account. An added device needs the
// root-signed list that drops it; a pending one only needs an added device.
func (s *AgentsService) RemoveDevice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, ok := requireAdmittedDevice(w, r)
		if !ok {
			return
		}
		var body struct {
			TargetDeviceID string              `json:"targetDeviceId"`
			List           *signedDeviceRecord `json:"list,omitempty"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		if !deviceIDPattern.MatchString(body.TargetDeviceID) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		var change *db.DeviceListChange
		if body.List != nil {
			root, err := s.database.DeviceVaultRoot(r.Context(), device.UserID)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			parsed, accountID, epoch, err := parseDeviceList(*body.List, root.RootPublicKey)
			if err != nil || accountID != device.UserID || parsed.VaultID != root.VaultID || epoch != root.KeyEpoch {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_device_list", "message": "The new device list could not be verified."})
				return
			}
			change = parsed
		}
		if _, err := s.database.RemoveDevice(r.Context(), device.UserID, body.TargetDeviceID, change); err != nil {
			writeAgentError(w, err)
			return
		}
		s.devices().kick(device.UserID, body.TargetDeviceID)
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	}
}

// parseStoredAdmissionRequest re-reads a stored request; its age is not checked
// again because the row's own expiry bounds it.
func parseStoredAdmissionRequest(request *db.DeviceAdmissionRequest, key []byte) (*deviceAdmissionRequest, error) {
	record := signedDeviceRecord{Payload: base64.StdEncoding.EncodeToString(request.RequestPayload), Signature: request.RequestSignature}
	payload, fields, err := record.verify(key, deviceAdmissionDomain)
	if err != nil {
		return nil, err
	}
	parsed := &deviceAdmissionRequest{Payload: payload}
	if err := decodeDeviceFields(fields, &parsed.AccountID, &parsed.DeviceID, &parsed.PublicKey, &parsed.X25519Public, &parsed.Commitment, &parsed.IssuedAt); err != nil {
		return nil, err
	}
	return parsed, nil
}
