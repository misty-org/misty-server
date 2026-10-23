package api

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// The device hub keeps presence in memory only: whether each of an account's
// devices is online right now, never a history, dropped when its socket
// closes. Devices never connect to each other; everything between them goes
// through the server, and this channel carries only presence and hints
// (docs/sync/devices.md).

const (
	deviceLivenessInterval  = 45 * time.Second
	deviceRemotePresenceTTL = 2 * time.Minute
	maxDeviceSocketsAccount = 30
	deviceNotifyChannel     = "misty_device_channel"
)

type devicePresence struct {
	DeviceID string `json:"deviceId"`
	Online   bool   `json:"online"`
}

type deviceConnection struct {
	id       string
	userID   string
	deviceID string
	admitted bool
	send     func(any) bool
	close    func()
	revoke   func()
	presence devicePresence
}

// deviceHubEvent crosses processes through Postgres NOTIFY. Payloads stay well
// under its 8000-byte limit.
type deviceHubEvent struct {
	Instance string          `json:"instance"`
	Kind     string          `json:"kind"`
	UserID   string          `json:"userId"`
	DeviceID string          `json:"deviceId"`
	Presence *devicePresence `json:"presence,omitempty"`
	// Entries carries a batch of presences ("presence-batch").
	Entries []hubPresenceEntry `json:"entries,omitempty"`
}

type hubPresenceEntry struct {
	UserID   string         `json:"u"`
	Presence devicePresence `json:"p"`
}

// presenceBatchSize keeps one batched NOTIFY well under Postgres's 8000 bytes.
const presenceBatchSize = 30

type remotePresence struct {
	presence devicePresence
	expires  time.Time
}

type deviceHub struct {
	database *db.Database
	instance string
	mu       sync.Mutex
	accounts map[string]map[string]*deviceConnection
	remote   map[string]map[string]remotePresence
	started  sync.Once
	// remoteSeen is when another API process last spoke. With none, nothing is
	// re-announced: a single process sends no notifications while idle.
	remoteSeen time.Time
}

func newDeviceHub(database *db.Database) *deviceHub {
	return &deviceHub{database: database, instance: uuid.NewString(),
		accounts: map[string]map[string]*deviceConnection{}, remote: map[string]map[string]remotePresence{}}
}

// start runs the liveness batch and the cross-process listener once.
func (h *deviceHub) start(ctx context.Context) {
	h.started.Do(func() {
		go h.runLiveness(ctx)
		go h.listen(ctx)
	})
}

func (h *deviceHub) register(connection *deviceConnection) bool {
	h.mu.Lock()
	devices := h.accounts[connection.userID]
	if devices == nil {
		devices = map[string]*deviceConnection{}
		h.accounts[connection.userID] = devices
	}
	if len(devices) >= maxDeviceSocketsAccount {
		h.mu.Unlock()
		return false
	}
	previous := devices[connection.deviceID]
	devices[connection.deviceID] = connection
	connection.presence.Online = true
	presence, admitted := connection.presence, connection.admitted
	h.mu.Unlock()
	// One socket per device: a new one replaces the old.
	if previous != nil {
		previous.close()
	}
	h.publishPresence(connection.userID, presence, admitted)
	return true
}

func (h *deviceHub) unregister(connection *deviceConnection) {
	h.mu.Lock()
	devices := h.accounts[connection.userID]
	current := devices != nil && devices[connection.deviceID] == connection
	if current {
		delete(devices, connection.deviceID)
		if len(devices) == 0 {
			delete(h.accounts, connection.userID)
		}
		connection.presence.Online = false
	}
	presence, admitted := connection.presence, connection.admitted
	h.mu.Unlock()
	if current {
		h.publishPresence(connection.userID, presence, admitted)
	}
}

// admit marks a pending device's socket as added once its admission lands.
func (h *deviceHub) admit(connection *deviceConnection) bool {
	h.mu.Lock()
	if connection.admitted {
		h.mu.Unlock()
		return false
	}
	connection.admitted = true
	presence := connection.presence
	h.mu.Unlock()
	h.publishPresence(connection.userID, presence, true)
	return true
}

func (h *deviceHub) isAdmitted(connection *deviceConnection) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return connection.admitted
}

// snapshot lists the account's online devices, local and on other processes.
func (h *deviceHub) snapshot(userID string) []devicePresence {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []devicePresence{}
	seen := map[string]bool{}
	for id, connection := range h.accounts[userID] {
		if connection.admitted {
			out = append(out, connection.presence)
			seen[id] = true
		}
	}
	now := time.Now()
	for id, entry := range h.remote[userID] {
		if !seen[id] && entry.expires.After(now) && entry.presence.Online {
			out = append(out, entry.presence)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeviceID < out[j].DeviceID })
	return out
}

func (h *deviceHub) localPeers(userID, except string) []*deviceConnection {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []*deviceConnection{}
	for id, connection := range h.accounts[userID] {
		if id != except && connection.admitted {
			out = append(out, connection)
		}
	}
	return out
}

func (h *deviceHub) local(userID, deviceID string) *deviceConnection {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.accounts[userID][deviceID]
}

// kick closes a removed device's socket wherever it is.
func (h *deviceHub) kick(userID, deviceID string) {
	if connection := h.local(userID, deviceID); connection != nil {
		connection.revoke()
	}
	h.notify(deviceHubEvent{Kind: "kick", UserID: userID, DeviceID: deviceID})
}
