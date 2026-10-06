package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func uuidString() string { return uuid.NewString() }

// Load check for the device channel (docs/plans/devices-unification.md): many
// idle sockets cost no database work between liveness batches. Opt in with
// MISTY_DEVICE_LOAD_TEST=5000 (the socket count).
func TestDeviceChannelIdleSocketsCostNoDatabaseWork(t *testing.T) {
	count, _ := strconv.Atoi(os.Getenv("MISTY_DEVICE_LOAD_TEST"))
	if count <= 0 {
		t.Skip("set MISTY_DEVICE_LOAD_TEST to the number of sockets")
	}
	f := newDeviceFixture(t)
	server := f.channelServer()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/devices/channel?ticket="
	// Each account allows 30 sockets; spread devices 25 to an account.
	users := make([]string, (count+24)/25)
	for index := range users {
		user, err := f.database.CreateUserWithUsername("Load", "load"+strings.ReplaceAll(uuidString()[:13], "-", ""), uniqueTestEmail("load"+strconv.Itoa(index)), "password123")
		if err != nil {
			t.Fatal(err)
		}
		users[index] = user.ID
	}
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	conns := make([]*websocket.Conn, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 64)
	started := time.Now()
	for i := range count {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			public, key, _ := ed25519.GenerateKey(rand.Reader)
			publicText := base64.StdEncoding.EncodeToString(public)
			userID := users[i/25]
			device, err := f.database.RegisterUnifiedDevice(t.Context(), userID, "Load "+strconv.Itoa(i), publicText, hex.EncodeToString(public), "macos", "", "", "", nil)
			if err != nil {
				errs <- err
				return
			}
			token := make([]byte, 32)
			_, _ = rand.Read(token)
			ticket := base64.RawURLEncoding.EncodeToString(token)
			hash := sha256.Sum256([]byte(ticket))
			if err := f.database.CreateDeviceChannelTicket(t.Context(), userID, device.ID, hex.EncodeToString(hash[:]), ""); err != nil {
				errs <- err
				return
			}
			conn, _, err := websocket.DefaultDialer.Dial(url+ticket, nil)
			if err != nil {
				errs <- err
				return
			}
			var challenge map[string]any
			_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			if err := conn.ReadJSON(&challenge); err != nil {
				errs <- err
				return
			}
			proof, _ := json.Marshal([]any{"misty.device.channel.v1", userID, device.ID, challenge["instance"], challenge["challenge"]})
			if err := conn.WriteJSON(map[string]any{"type": "authenticate", "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(key, proof))}); err != nil {
				errs <- err
				return
			}
			var ready map[string]any
			if err := conn.ReadJSON(&ready); err != nil || ready["type"] != "ready" {
				errs <- err
				return
			}
			_ = conn.SetReadDeadline(time.Time{})
			conns[i] = conn
			// Reading answers the server's pings.
			go func() {
				for {
					if _, _, err := conn.ReadMessage(); err != nil {
						return
					}
				}
			}()
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("open socket: %v", err)
	}
	t.Logf("opened %d authenticated sockets in %s", count, time.Since(started).Round(time.Millisecond))
	var after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("heap per socket (client and server in one process): %d bytes", (after.HeapInuse-before.HeapInuse)/uint64(count))

	// Writes to device tables other than the liveness batch, the liveness
	// batch's own row updates, and cross-process notifications.
	counts := func() (int64, int64) {
		var other, liveness int64
		// A backend reports its pending statistics when it exits; closing
		// idle pool connections makes earlier work show up now, not later.
		f.database.Conn.SetMaxIdleConns(0)
		time.Sleep(time.Second)
		f.database.Conn.SetMaxIdleConns(2)
		_, _ = f.database.Conn.Exec(`SELECT pg_stat_clear_snapshot()`)
		if err := f.database.Conn.QueryRow(`SELECT COALESCE(SUM(CASE WHEN relname<>'trusted_devices' THEN n_tup_ins+n_tup_upd+n_tup_del END),0),
			COALESCE(SUM(CASE WHEN relname='trusted_devices' THEN n_tup_upd END),0)
			FROM pg_stat_user_tables WHERE relname LIKE 'device%' OR relname='trusted_devices'`).Scan(&other, &liveness); err != nil {
			t.Fatal(err)
		}
		return other, liveness
	}
	otherBefore, livenessBefore := counts()
	notificationsBefore := TestingDeviceHubNotifications()
	// Two 30-second pings and at most two 45-second liveness batches.
	time.Sleep(65 * time.Second)
	time.Sleep(12 * time.Second) // let the window's own stats land
	otherAfter, livenessAfter := counts()
	other, liveness := otherAfter-otherBefore, livenessAfter-livenessBefore
	notifications := TestingDeviceHubNotifications() - notificationsBefore
	t.Logf("while %d sockets idled for 65s: %d device-table writes, %d liveness row updates (one statement per 45s per process), %d notifications", count, other, liveness, notifications)
	if other != 0 || notifications != 0 {
		t.Fatalf("idle sockets cost %d device writes and %d notifications", other, notifications)
	}
	if liveness > 2*int64(count) {
		t.Fatalf("liveness updated %d rows for %d sockets in two intervals", liveness, count)
	}
	for _, conn := range conns {
		if conn != nil {
			_ = conn.Close()
		}
	}
}
