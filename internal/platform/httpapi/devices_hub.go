package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// The device hub keeps presence and LAN address candidates in memory only:
// the current entry per device, never a history, dropped when its socket
// closes. It routes small control messages between an account's devices and
// never carries file contents (docs/design/devices/BRIEF.md).

const (
	deviceLivenessInterval  = 45 * time.Second
	deviceRemotePresenceTTL = 2 * time.Minute
	maxDeviceCandidates     = 8
	maxDeviceSocketsAccount = 30
	deviceNotifyChannel     = "misty_device_channel"
)

type devicePresence struct {
	DeviceID   string   `json:"deviceId"`
	EndpointID string   `json:"endpointId,omitempty"`
	Online     bool     `json:"online"`
	NetworkKey string   `json:"networkKey,omitempty"`
	Overlay    bool     `json:"overlay,omitempty"`
	Candidates []string `json:"-"`
}

type deviceConnection struct {
	id         string
	userID     string
	deviceID   string
	admitted   bool
	send       func(any) bool
	close      func()
	revoke     func()
	presence   devicePresence
	connectLog map[string][]time.Time
}

// deviceHubEvent crosses processes through Postgres NOTIFY. Payloads stay well
// under its 8000-byte limit: at most eight candidate addresses.
type deviceHubEvent struct {
	Instance   string          `json:"instance"`
	Kind       string          `json:"kind"`
	UserID     string          `json:"userId"`
	DeviceID   string          `json:"deviceId"`
	Target     string          `json:"target,omitempty"`
	Presence   *devicePresence `json:"presence,omitempty"`
	Candidates []string        `json:"candidates,omitempty"`
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
	secret   []byte
	mu       sync.Mutex
	accounts map[string]map[string]*deviceConnection
	remote   map[string]map[string]remotePresence
	started  sync.Once
	// remoteSeen is when another API process last spoke. With none, nothing is
	// re-announced: a single process sends no notifications while idle.
	remoteSeen time.Time
}

func newDeviceHub(database *db.Database) *deviceHub {
	secret := []byte(envconfig.Getenv("MISTY_DEVICE_PAIRING_PEPPER"))
	if len(secret) < 32 {
		secret = make([]byte, 32)
		_, _ = rand.Read(secret)
	}
	return &deviceHub{database: database, instance: uuid.NewString(), secret: secret,
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
		connection.presence.Candidates = nil
	}
	presence, admitted := connection.presence, connection.admitted
	h.mu.Unlock()
	if current {
		h.publishPresence(connection.userID, presence, admitted)
	}
}

// setCandidates replaces a device's current LAN candidates.
func (h *deviceHub) setCandidates(connection *deviceConnection, candidates []string, overlay bool) {
	h.mu.Lock()
	connection.presence.Candidates = candidates
	connection.presence.Overlay = overlay
	presence, admitted := connection.presence, connection.admitted
	h.mu.Unlock()
	h.publishPresence(connection.userID, presence, admitted)
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

// localAdmitted returns an admitted local device and a copy of its presence.
func (h *deviceHub) localAdmitted(userID, deviceID string) (*deviceConnection, devicePresence, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	connection := h.accounts[userID][deviceID]
	if connection == nil || !connection.admitted {
		return nil, devicePresence{}, false
	}
	return connection, connection.presence, true
}

// publishPresence tells the account's other devices, here and elsewhere.
// Candidates never travel with presence; they go only with a connect intent.
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

// routeCandidates sends a device the other's LAN candidates. Both devices of a
// connect intent receive them at the same moment and dial each other.
func (h *deviceHub) routeCandidates(userID, toDevice, fromDevice, endpointID string, candidates []string) {
	message := map[string]any{"type": "candidates", "deviceId": fromDevice, "endpointId": endpointID, "addresses": candidates}
	if connection := h.local(userID, toDevice); connection != nil {
		connection.send(message)
		return
	}
	h.notify(deviceHubEvent{Kind: "candidates", UserID: userID, DeviceID: fromDevice, Target: toDevice, Candidates: candidates,
		Presence: &devicePresence{DeviceID: fromDevice, EndpointID: endpointID}})
}

// connectIntent answers "A wants B": B must be admitted and online. Returns false
// when B is offline everywhere.
func (h *deviceHub) connectIntent(from *deviceConnection, target string) bool {
	h.mu.Lock()
	own := from.presence
	h.mu.Unlock()
	if _, peer, ok := h.localAdmitted(from.userID, target); ok {
		h.routeCandidates(from.userID, from.deviceID, target, peer.EndpointID, peer.Candidates)
		h.routeCandidates(from.userID, target, from.deviceID, own.EndpointID, own.Candidates)
		return true
	}
	h.mu.Lock()
	remote, ok := h.remote[from.userID][target]
	h.mu.Unlock()
	if !ok || !remote.presence.Online || remote.expires.Before(time.Now()) {
		return false
	}
	// The other process answers with B's candidates and forwards A's to B.
	h.notify(deviceHubEvent{Kind: "connect", UserID: from.userID, DeviceID: from.deviceID, Target: target, Candidates: own.Candidates,
		Presence: &devicePresence{DeviceID: from.deviceID, EndpointID: own.EndpointID}})
	return true
}

// kick closes a removed device's socket wherever it is.
func (h *deviceHub) kick(userID, deviceID string) {
	if connection := h.local(userID, deviceID); connection != nil {
		connection.revoke()
	}
	h.notify(deviceHubEvent{Kind: "kick", UserID: userID, DeviceID: deviceID})
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
	case "candidates":
		if connection := h.local(event.UserID, event.Target); connection != nil && event.Presence != nil {
			connection.send(map[string]any{"type": "candidates", "deviceId": event.DeviceID, "endpointId": event.Presence.EndpointID, "addresses": event.Candidates})
		}
	case "connect":
		peer, presence, ok := h.localAdmitted(event.UserID, event.Target)
		if !ok || event.Presence == nil {
			return
		}
		peer.send(map[string]any{"type": "candidates", "deviceId": event.DeviceID, "endpointId": event.Presence.EndpointID, "addresses": event.Candidates})
		h.notify(deviceHubEvent{Kind: "candidates", UserID: event.UserID, DeviceID: peer.deviceID, Target: event.DeviceID, Candidates: presence.Candidates,
			Presence: &devicePresence{DeviceID: peer.deviceID, EndpointID: presence.EndpointID}})
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
