package browsersync

import (
	"bytes"
	"context"
	"encoding/json"
	"time"
)

// presence pushes the device roster, the online set and, for protocol v2, the
// workspace roster. Each is sent only when it changed.
func (w *connectionWriter) presence() error {
	bounded, stop := context.WithTimeout(w.ctx, 5*time.Second)
	defer stop()
	if err := w.pushDevices(bounded); err != nil {
		return err
	}
	if err := w.pushOnline(bounded); err != nil {
		return err
	}
	if w.watches == nil {
		return nil
	}
	return w.pushRoster(bounded)
}

// pushDevices fails the connection when this device has been revoked.
func (w *connectionWriter) pushDevices(ctx context.Context) error {
	devices, err := w.service.store.BrowserSyncDevices(ctx, w.identity.UserID, w.identity.VaultID)
	if err != nil {
		return err
	}
	allowed := false
	for _, d := range devices {
		if d.DeviceID == w.identity.DeviceID && d.RevokedAt == nil {
			allowed = true
		}
	}
	if !allowed {
		return ErrSyncForbidden
	}
	raw, _ := json.Marshal(devices)
	if !bytes.Equal(raw, w.lastDevices) {
		if err := w.write(map[string]any{"type": "devices", "devices": devices}); err != nil {
			return err
		}
		w.lastDevices = raw
	}
	return nil
}

// pushOnline sends the online device set when it changed.
func (w *connectionWriter) pushOnline(ctx context.Context) error {
	online, err := w.service.store.BrowserSyncPresence(ctx, w.identity.UserID, w.identity.VaultID)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(online)
	if !bytes.Equal(raw, w.lastPresence) {
		if err := w.write(map[string]any{"type": "presence", "devices": online}); err != nil {
			return err
		}
		w.lastPresence = raw
	}
	return nil
}

// pushRoster sends the workspace roster when it changed.
func (w *connectionWriter) pushRoster(ctx context.Context) error {
	roster, err := w.service.store.BrowserSyncWorkspaces(ctx, w.identity.UserID, w.identity.VaultID)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(roster)
	if !bytes.Equal(raw, w.lastWorkspaces) {
		if err := w.write(map[string]any{"type": "workspaces", "workspaces": roster}); err != nil {
			return err
		}
		w.lastWorkspaces = raw
	}
	return nil
}
