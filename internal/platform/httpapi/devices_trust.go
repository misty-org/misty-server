package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// Devices: admission, signed list, policies, names and removal
// (docs/sync/devices.md). Every change that affects trust carries a
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
