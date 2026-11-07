package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// Signed-in sessions' names, policies and removal.

// RenameDevice sets one device's name.
func (s *DevicesService) RenameDevice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, ok := requestDevice(r)
		if !ok {
			return
		}
		target := chi.URLParam(r, "targetID")
		var body struct {
			Name string `json:"name"`
		}
		if decodeStrictJSON(w, r, &body) != nil {
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		if !deviceIDPattern.MatchString(target) || !validDeviceName(body.Name) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if target != device.DeviceID && device.State != "admitted" {
			writeDeviceError(w, db.ErrDeviceNotAdmitted)
			return
		}
		err := s.database.RenameDevice(r.Context(), device.UserID, target, body.Name)
		writeDeviceResult(w, map[string]string{"name": body.Name}, err, http.StatusOK)
	}
}

// StoreDevicePolicy records a device's own signed permissions. Only the device
// itself can sign them, so permissions change only on that device.
func (s *DevicesService) StoreDevicePolicy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, ok := requestDevice(r)
		if !ok {
			return
		}
		var body struct {
			Policy signedDeviceRecord `json:"policy"`
		}
		if decodeStrictJSON(w, r, &body) != nil {
			return
		}
		policy, err := parseDevicePolicy(body.Policy, device.Key)
		if err != nil || policy.AccountID != device.UserID || policy.DeviceID != device.DeviceID {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		err = s.database.StoreDevicePolicy(r.Context(), device.UserID, device.DeviceID, policy.Record)
		writeDeviceResult(w, map[string]any{"version": policy.Record.Version}, err, http.StatusOK)
	}
}

// RemoveDevice removes a signed-in session for good.
func (s *DevicesService) RemoveDevice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, ok := requireAdmittedDevice(w, r)
		if !ok {
			return
		}
		var body struct {
			TargetDeviceID string `json:"targetDeviceId"`
		}
		if decodeStrictJSON(w, r, &body) != nil {
			return
		}
		if !deviceIDPattern.MatchString(body.TargetDeviceID) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if _, err := s.database.RemoveDevice(r.Context(), device.UserID, body.TargetDeviceID); err != nil {
			writeDeviceError(w, err)
			return
		}
		s.devices().kick(device.UserID, body.TargetDeviceID)
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	}
}
