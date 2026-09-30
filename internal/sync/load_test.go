package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	mathrand "math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// TestBrowserSyncLoad drives many real protocol clients against two server
// instances sharing one PostgreSQL, the way production fans out: each account
// has two devices on different instances, each watching the other's workspace, and
// a fraction of clients publish a signed edit on a fixed cadence.
//
// Opt-in; needs the disposable sync test database and a raised file limit:
//
//	ulimit -n 65536
//	MISTY_SYNC_LOAD_CLIENTS=10000 go test ./internal/sync -run TestBrowserSyncLoad -timeout 30m -v
//
// Knobs (env): MISTY_SYNC_LOAD_CLIENTS (10000), _ACTIVE (0.2), _INTERVAL (5s),
// _DURATION (60s), _POOL (64 connections per instance), _ASSERT=1 to fail on
// the targets (p99 delta latency 150ms, no errors), _REPORT=<path> for JSON.
func TestBrowserSyncLoad(t *testing.T) {
	clients := loadEnvInt("MISTY_SYNC_LOAD_CLIENTS", 0)
	if clients == 0 {
		t.Skip("set MISTY_SYNC_LOAD_CLIENTS to run the load test")
	}
	clients -= clients % 2
	active := loadEnvFloat("MISTY_SYNC_LOAD_ACTIVE", 0.2)
	interval := loadEnvDuration("MISTY_SYNC_LOAD_INTERVAL", 5*time.Second)
	duration := loadEnvDuration("MISTY_SYNC_LOAD_DURATION", 60*time.Second)
	pool := loadEnvInt("MISTY_SYNC_LOAD_POOL", 64)

	database, peer := browserSocketTestDatabase(t)
	database.Conn.SetMaxOpenConns(pool)
	peer.Conn.SetMaxOpenConns(pool)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	servers := make([]string, 2)
	for i, instance := range []*db.Database{database, peer} {
		mux := http.NewServeMux()
		mux.Handle("/ws", NewBrowserSyncService(instance).Connect())
		server := httptest.NewServer(mux)
		defer server.Close()
		servers[i] = "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	}

	t.Logf("enrolling %d accounts (%d devices)", clients/2, clients)
	accounts := make([]*loadAccount, clients/2)
	setup := time.Now()
	loadParallel(t, len(accounts), 32, func(i int) error {
		account, err := enrollLoadAccount(ctx, NewStore(database.Conn), fmt.Sprintf("load-%d", i))
		accounts[i] = account
		return err
	})
	t.Logf("enrolled in %s", time.Since(setup).Round(time.Millisecond))

	stats := &loadStats{}
	setup = time.Now()
	loadParallel(t, len(accounts), 200, func(i int) error {
		for d, device := range accounts[i].devices {
			if err := device.connect(ctx, database, servers[d], stats); err != nil {
				return err
			}
		}
		return nil
	})
	t.Logf("connected %d clients in %s", clients, time.Since(setup).Round(time.Millisecond))

	before := loadDatabaseCounters(t, database)
	var wg sync.WaitGroup
	deadline := time.Now().Add(duration)
	for _, account := range accounts {
		for _, device := range account.devices {
			if mathrand.Float64() >= active {
				continue
			}
			wg.Add(1)
			go func(device *loadDevice) {
				defer wg.Done()
				// Spread the first edit over one interval: no synchronized burst.
				time.Sleep(time.Duration(mathrand.Int64N(int64(interval))))
				for time.Now().Before(deadline) {
					device.publish(stats)
					time.Sleep(interval)
				}
			}(device)
		}
	}
	wg.Wait()
	// Let the last deltas arrive.
	time.Sleep(2 * time.Second)
	after := loadDatabaseCounters(t, database)
	for _, account := range accounts {
		for _, device := range account.devices {
			device.close()
		}
	}

	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	report := stats.report(duration, clients, after.minus(before), memory.HeapAlloc)
	encoded, _ := json.MarshalIndent(report, "", "  ")
	t.Logf("load report:\n%s", encoded)
	if path := os.Getenv("MISTY_SYNC_LOAD_REPORT"); path != "" {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("MISTY_SYNC_LOAD_ASSERT") == "1" {
		if report.DeltaP99Ms > 150 || report.Errors > 0 || report.Deltas == 0 {
			t.Fatalf("targets missed: p99 delta %.1fms (target 150), %d errors, %d deltas", report.DeltaP99Ms, report.Errors, report.Deltas)
		}
	}
}

type loadAccount struct {
	user    string
	devices [2]*loadDevice
}

type loadDevice struct {
	user    string
	grant   SyncDeviceGrant
	key     ed25519.PrivateKey
	peer    *loadDevice
	conn    *websocket.Conn
	counter int64
	version atomic.Int64
	// Publish send times by the workspace version they produce, for the peer.
	sent sync.Map
	// Request id → publish time, for ack latency.
	acks sync.Map
}

func enrollLoadAccount(ctx context.Context, store *Store, user string) (*loadAccount, error) {
	if _, err := store.Conn.ExecContext(ctx, `INSERT INTO users VALUES($1) ON CONFLICT DO NOTHING`, user); err != nil {
		return nil, err
	}
	root, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	a, keyA := socketTestGrant(uuid.NewString(), rootKey)
	b, keyB := socketTestGrant(a.VaultID, rootKey)
	wrapper := SyncKeyEnvelope{Version: 1, KDF: "argon2id-m65536-t3-p1", Salt: base64.StdEncoding.EncodeToString(make([]byte, 16)), Nonce: base64.StdEncoding.EncodeToString(make([]byte, 12)), Ciphertext: base64.StdEncoding.EncodeToString(make([]byte, 32))}
	if err := store.CreateBrowserSyncVault(ctx, user, root, wrapper, a); err != nil {
		return nil, err
	}
	if err := store.EnrollBrowserSyncDevice(ctx, user, b); err != nil {
		return nil, err
	}
	if err := store.EnableBrowserSyncWorkspaceMode(ctx, user, a.VaultID); err != nil {
		return nil, err
	}
	account := &loadAccount{user: user}
	account.devices[0] = &loadDevice{user: user, grant: a, key: keyA}
	account.devices[1] = &loadDevice{user: user, grant: b, key: keyB}
	account.devices[0].peer, account.devices[1].peer = account.devices[1], account.devices[0]
	return account, nil
}

func (d *loadDevice) connect(ctx context.Context, database *db.Database, base string, stats *loadStats) error {
	token, err := security.GenerateSecureToken()
	if err != nil {
		return err
	}
	if err = NewStore(database.Conn).CreateBrowserSyncTicket(ctx, d.user, d.grant.VaultID, d.grant.DeviceID, security.HashToken(token)); err != nil {
		return err
	}
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	conn, _, err := dialer.Dial(base+"?protocol=3&ticket="+token, nil)
	if err != nil {
		return err
	}
	var challenge struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
	}
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	if err = conn.ReadJSON(&challenge); err != nil || challenge.Type != "challenge" {
		conn.Close()
		return fmt.Errorf("challenge: %v %q", err, challenge.Type)
	}
	proof := ed25519.Sign(d.key, syncConnectionProof(d.grant.VaultID, d.grant.DeviceID, challenge.Challenge))
	if err = conn.WriteJSON(map[string]any{"type": "authenticate", "after": 0, "signature": proof}); err != nil {
		conn.Close()
		return err
	}
	_ = conn.SetReadDeadline(time.Time{})
	d.conn = conn
	for _, workspace := range []string{d.grant.DeviceID, d.peer.grant.DeviceID} {
		if err = conn.WriteJSON(map[string]any{"type": "watch_workspace", "workspace_id": workspace, "after": 0}); err != nil {
			return err
		}
	}
	go d.read(stats)
	return nil
}

// read handles every frame on the connection: acks of this device's
// publishes, and deltas of its peer's workspace (whose publish times it reads).
func (d *loadDevice) read(stats *loadStats) {
	for {
		var frame struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Code      string `json:"code"`
			Receipt   *struct {
				Discarded        bool   `json:"discarded"`
				Reason           string `json:"reason"`
				WorkspaceVersion int64  `json:"workspace_version"`
			} `json:"receipt"`
			Delta *struct {
				WorkspaceID string `json:"workspace_id"`
				Version     int64  `json:"version"`
			} `json:"delta"`
		}
		if err := d.conn.ReadJSON(&frame); err != nil {
			return
		}
		now := time.Now()
		switch frame.Type {
		case "workspace_ack":
			if sent, ok := d.acks.LoadAndDelete(frame.RequestID); ok {
				stats.ack(now.Sub(sent.(time.Time)))
			}
			if frame.Receipt != nil {
				if frame.Receipt.Discarded {
					stats.discards.Add(1)
				}
				if frame.Receipt.WorkspaceVersion > 0 {
					d.version.Store(frame.Receipt.WorkspaceVersion)
				}
			}
		case "workspace_error":
			stats.errors.Add(1)
			d.acks.Delete(frame.RequestID)
		case "workspace_delta":
			if frame.Delta != nil && frame.Delta.WorkspaceID == d.peer.grant.DeviceID {
				if sent, ok := d.peer.sent.LoadAndDelete(frame.Delta.Version); ok {
					stats.delta(now.Sub(sent.(time.Time)))
				}
			}
		}
	}
}

// publish sends one signed edit: the workspace's root node plus one tab-sized node.
func (d *loadDevice) publish(stats *loadStats) {
	base := d.version.Load()
	d.counter++
	tab := uuid.NewString()
	root := d.grant.DeviceID
	ciphertext := make([]byte, 240)
	_, _ = rand.Read(ciphertext)
	op := SyncWorkspaceOp{
		VaultID: d.grant.VaultID, WorkspaceID: root, OperationID: uuid.NewString(),
		DeviceID: d.grant.DeviceID, DeviceCounter: d.counter, KeyEpoch: 1,
		BaseWorkspaceVersion: base, MerkleRoot: make([]byte, 32),
		Upserts: []SyncNodeWrite{{NodeID: root, Ciphertext: make([]byte, 64)}, {NodeID: tab, ParentID: &root, Ciphertext: ciphertext}},
	}
	op.Signature = ed25519.Sign(d.key, op.SigningBytes())
	request := uuid.NewString()
	now := time.Now()
	d.acks.Store(request, now)
	d.sent.Store(base+1, now)
	if err := d.conn.WriteJSON(map[string]any{"type": "publish_workspace", "request_id": request, "workspace_op": op}); err != nil {
		stats.errors.Add(1)
		return
	}
	stats.publishes.Add(1)
}

func (d *loadDevice) close() {
	if d.conn != nil {
		_ = d.conn.Close()
	}
}

type loadStats struct {
	mu                          sync.Mutex
	acks, deltas                []time.Duration
	publishes, errors, discards atomic.Int64
}

func (s *loadStats) ack(d time.Duration) {
	s.mu.Lock()
	s.acks = append(s.acks, d)
	s.mu.Unlock()
}

func (s *loadStats) delta(d time.Duration) {
	s.mu.Lock()
	s.deltas = append(s.deltas, d)
	s.mu.Unlock()
}

type loadReport struct {
	Clients        int     `json:"clients"`
	Seconds        float64 `json:"seconds"`
	Publishes      int64   `json:"publishes"`
	PublishesPerS  float64 `json:"publishes_per_second"`
	Discards       int64   `json:"discards"`
	Errors         int64   `json:"errors"`
	AckP50Ms       float64 `json:"ack_p50_ms"`
	AckP99Ms       float64 `json:"ack_p99_ms"`
	Deltas         int     `json:"deltas"`
	DeltaP50Ms     float64 `json:"delta_p50_ms"`
	DeltaP95Ms     float64 `json:"delta_p95_ms"`
	DeltaP99Ms     float64 `json:"delta_p99_ms"`
	CommitsPerS    float64 `json:"db_commits_per_second"`
	WALSyncsPerS   float64 `json:"db_wal_syncs_per_second"`
	CommitsPerSync float64 `json:"db_commits_per_wal_sync"`
	HeapMB         float64 `json:"process_heap_mb"`
}

func (s *loadStats) report(duration time.Duration, clients int, dbDelta loadCounters, heap uint64) loadReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	seconds := duration.Seconds()
	r := loadReport{
		Clients: clients, Seconds: seconds,
		Publishes: s.publishes.Load(), Discards: s.discards.Load(), Errors: s.errors.Load(),
		AckP50Ms: percentile(s.acks, 0.50), AckP99Ms: percentile(s.acks, 0.99),
		Deltas:     len(s.deltas),
		DeltaP50Ms: percentile(s.deltas, 0.50), DeltaP95Ms: percentile(s.deltas, 0.95), DeltaP99Ms: percentile(s.deltas, 0.99),
		CommitsPerS: float64(dbDelta.commits) / seconds, WALSyncsPerS: float64(dbDelta.walSyncs) / seconds,
		HeapMB: float64(heap) / (1 << 20),
	}
	r.PublishesPerS = float64(r.Publishes) / seconds
	if dbDelta.walSyncs > 0 {
		// Above 1: PostgreSQL already flushes several commits together.
		r.CommitsPerSync = float64(dbDelta.commits) / float64(dbDelta.walSyncs)
	}
	return r
}

func percentile(values []time.Duration, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(math.Ceil(p*float64(len(sorted)))) - 1
	return float64(sorted[max(index, 0)].Microseconds()) / 1000
}

type loadCounters struct{ commits, walSyncs int64 }

func (c loadCounters) minus(o loadCounters) loadCounters {
	return loadCounters{c.commits - o.commits, c.walSyncs - o.walSyncs}
}

// loadDatabaseCounters reads commit and WAL flush totals: their ratio shows
// how many commits PostgreSQL already groups into one disk flush.
func loadDatabaseCounters(t *testing.T, database *db.Database) loadCounters {
	t.Helper()
	var c loadCounters
	if err := database.Conn.QueryRow(`SELECT xact_commit FROM pg_stat_database WHERE datname=current_database()`).Scan(&c.commits); err != nil {
		t.Fatal(err)
	}
	// pg_stat_wal exists from PostgreSQL 14; without it the ratio is omitted.
	_ = database.Conn.QueryRow(`SELECT wal_sync FROM pg_stat_wal`).Scan(&c.walSyncs)
	return c
}

func loadParallel(t *testing.T, n, width int, work func(int) error) {
	t.Helper()
	var wg sync.WaitGroup
	var failed atomic.Pointer[error]
	next := atomic.Int64{}
	for range width {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= n || failed.Load() != nil {
					return
				}
				if err := work(i); err != nil {
					failed.CompareAndSwap(nil, &err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := failed.Load(); err != nil {
		t.Fatal(*err)
	}
}

func loadEnvInt(name string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return fallback
}

func loadEnvFloat(name string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(name), 64); err == nil {
		return v
	}
	return fallback
}

func loadEnvDuration(name string, fallback time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(name)); err == nil {
		return v
	}
	return fallback
}
