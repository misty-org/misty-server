package api

import (
	"context"
	"encoding/json"
	"log"
	"sync/atomic"
	"time"
)

// publishPresence tells the account's other devices, here and elsewhere.
func (h *deviceHub) publishPresence(userID string, presence devicePresence, admitted bool) {
	if !admitted {
		return
	}
	message := map[string]any{"type": "presence.delta", "device": presence}
	for _, peer := range h.localPeers(userID, presence.DeviceID) {
		peer.send(message)
	}
	// Other processes hear changes only when one exists; a lone process
	// makes no database round trip for presence.
	h.mu.Lock()
	remote := time.Since(h.remoteSeen) < 3*time.Minute
	h.mu.Unlock()
	if remote {
		h.notify(deviceHubEvent{Kind: "presence", UserID: userID, DeviceID: presence.DeviceID, Presence: &presence})
	}
}

// deviceHubNotifications counts every cross-process notification sent.
var deviceHubNotifications atomic.Int64

// TestingDeviceHubNotifications reports how many notifications hubs sent.
func TestingDeviceHubNotifications() int64 { return deviceHubNotifications.Load() }

func (h *deviceHub) notify(event deviceHubEvent) {
	deviceHubNotifications.Add(1)
	event.Instance = h.instance
	payload, err := json.Marshal(event)
	if err != nil || len(payload) > 7000 || h.database == nil || h.database.Conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = h.database.Conn.ExecContext(ctx, `SELECT pg_notify($1,$2)`, deviceNotifyChannel, string(payload))
}

func (h *deviceHub) receive(event deviceHubEvent) {
	if event.Instance == h.instance || event.UserID == "" || event.DeviceID == "" {
		return
	}
	h.mu.Lock()
	h.remoteSeen = time.Now()
	h.mu.Unlock()
	switch event.Kind {
	case "hello":
		h.announceAll()
	case "presence-batch":
		for _, entry := range event.Entries {
			presence := entry.Presence
			h.receive(deviceHubEvent{Instance: event.Instance, Kind: "presence", UserID: entry.UserID, DeviceID: presence.DeviceID, Presence: &presence})
		}
	case "presence":
		if event.Presence == nil {
			return
		}
		h.mu.Lock()
		if h.remote[event.UserID] == nil {
			h.remote[event.UserID] = map[string]remotePresence{}
		}
		h.remote[event.UserID][event.DeviceID] = remotePresence{presence: *event.Presence, expires: time.Now().Add(deviceRemotePresenceTTL)}
		h.mu.Unlock()
		for _, peer := range h.localPeers(event.UserID, event.DeviceID) {
			peer.send(map[string]any{"type": "presence.delta", "device": event.Presence})
		}
	case "kick":
		if connection := h.local(event.UserID, event.DeviceID); connection != nil {
			connection.revoke()
		}
	}
}

func (h *deviceHub) connectedDeviceIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []string{}
	for _, devices := range h.accounts {
		for id := range devices {
			out = append(out, id)
		}
	}
	return out
}

// runLiveness is the hub's only periodic database write: one statement per
// process that keeps the online window current for every open socket, and a
// presence re-announcement for other processes.
func (h *deviceHub) runLiveness(ctx context.Context) {
	ticker := time.NewTicker(deviceLivenessInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ids := h.connectedDeviceIDs()
			bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := h.database.MarkDevicesSeen(bounded, ids); err != nil && ctx.Err() == nil {
				log.Printf("device liveness renewal failed: %v", err)
			}
			cancel()
			h.mu.Lock()
			now := time.Now()
			for user, devices := range h.remote {
				for id, entry := range devices {
					if entry.expires.Before(now) {
						delete(devices, id)
					}
				}
				if len(devices) == 0 {
					delete(h.remote, user)
				}
			}
			remoteRecently := time.Since(h.remoteSeen) < 3*time.Minute
			h.mu.Unlock()
			if remoteRecently {
				h.announceAll()
			}
		}
	}
}

// announceAll re-announces this process's admitted devices to other processes
// in batches, so their snapshots stay current.
func (h *deviceHub) announceAll() {
	h.mu.Lock()
	entries := []hubPresenceEntry{}
	for user, devices := range h.accounts {
		for _, connection := range devices {
			if connection.admitted {
				entries = append(entries, hubPresenceEntry{UserID: user, Presence: connection.presence})
			}
		}
	}
	h.mu.Unlock()
	for start := 0; start < len(entries); start += presenceBatchSize {
		end := min(start+presenceBatchSize, len(entries))
		h.notify(deviceHubEvent{Kind: "presence-batch", UserID: "-", DeviceID: "-", Entries: entries[start:end]})
	}
}

func (h *deviceHub) listen(ctx context.Context) {
	if h.database == nil {
		return
	}
	for ctx.Err() == nil {
		events, stop, err := h.database.ListenNotifications(ctx, deviceNotifyChannel)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				continue
			}
		}
		// Other processes answer a hello with their presence, batched.
		h.notify(deviceHubEvent{Kind: "hello", UserID: "-", DeviceID: "-"})
		for payload := range events {
			var event deviceHubEvent
			if json.Unmarshal([]byte(payload), &event) == nil {
				h.receive(event)
			}
		}
		stop()
	}
}
