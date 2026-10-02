package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// socketTestClosed waits for the server to end the socket.
func socketTestClosed(t *testing.T, c *websocket.Conn, within time.Duration) error {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(within))
	for {
		if _, _, err := c.ReadMessage(); err != nil {
			var timeout interface{ Timeout() bool }
			if errors.As(err, &timeout) && timeout.Timeout() {
				t.Fatal("socket stayed open")
			}
			return err
		}
	}
}

func socketTestVault(t *testing.T, store *Store) (SyncDeviceGrant, ed25519.PrivateKey) {
	t.Helper()
	root, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	a, keyA := socketTestGrant(uuid.NewString(), rootKey)
	wrapper := SyncKeyEnvelope{Version: 1, KDF: "argon2id-m65536-t3-p1", Salt: base64.StdEncoding.EncodeToString(make([]byte, 16)), Nonce: base64.StdEncoding.EncodeToString(make([]byte, 12)), Ciphertext: base64.StdEncoding.EncodeToString(make([]byte, 32))}
	if err := store.CreateBrowserSyncVault(context.Background(), "owner", root, wrapper, a); err != nil {
		t.Fatal(err)
	}
	return a, keyA
}

func TestBrowserSyncSocketIdleWritesNothingAndFencesOnLapse(t *testing.T) {
	database, _ := browserSocketTestDatabase(t)
	store := NewStore(database.Conn)
	a, keyA := socketTestVault(t, store)
	service := NewBrowserSyncService(database)
	server := httptest.NewServer(service.Connect())
	defer server.Close()
	c, _ := socketTestDial(t, database, "ws"+strings.TrimPrefix(server.URL, "http"), a, keyA, 0)
	defer c.Close()
	socketTestRead(t, c, "welcome")
	var before int64
	if err := database.Conn.QueryRow(`SELECT n_tup_upd+n_tup_ins FROM pg_stat_xact_user_tables WHERE relname='browser_sync_connections'`).Scan(&before); err != nil && !strings.Contains(err.Error(), "no rows") {
		t.Fatal(err)
	}
	// Repeated unchanged heartbeats record nothing.
	for range 5 {
		if err := c.WriteJSON(map[string]any{"type": "heartbeat", "applied_sequence": 0, "ready": false}); err != nil {
			t.Fatal(err)
		}
	}
	var writes int
	time.Sleep(200 * time.Millisecond)
	if err := database.Conn.QueryRow(`SELECT count(*) FROM browser_sync_connections WHERE last_seen_at>clock_timestamp()-interval '150 milliseconds'`).Scan(&writes); err != nil || writes != 0 {
		t.Fatal("unchanged heartbeat wrote liveness", writes, err)
	}
	if presence, err := store.BrowserSyncPresence(context.Background(), "owner", a.VaultID); err != nil || len(presence) != 1 || !presence[0].Online {
		t.Fatal("idle connection offline", presence, err)
	}
	// A process that could not renew its lease closes its sockets itself.
	service.fenceConnections()
	if err := socketTestClosed(t, c, 3*time.Second); err == nil {
		t.Fatal("fenced socket open")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		presence, err := store.BrowserSyncPresence(context.Background(), "owner", a.VaultID)
		if err == nil && len(presence) == 1 && !presence[0].Online {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fenced device still online", presence, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBrowserSyncSocketRevalidatesSessionInBand(t *testing.T) {
	database, _ := browserSocketTestDatabase(t)
	if _, err := database.Conn.Exec(`ALTER TABLE users ADD COLUMN lifecycle_state text DEFAULT 'active';
 CREATE TABLE sessions(token_hash text PRIMARY KEY,user_id text,expires_at timestamptz)`); err != nil {
		t.Fatal(err)
	}
	store := NewStore(database.Conn)
	a, keyA := socketTestVault(t, store)
	session := security.HashToken("session-" + uuid.NewString())
	if _, err := database.Conn.Exec(`INSERT INTO sessions VALUES($1,'owner',clock_timestamp()+interval '1 hour')`, session); err != nil {
		t.Fatal(err)
	}
	service := NewBrowserSyncService(database)
	service.revalidateEvery = 100 * time.Millisecond
	server := httptest.NewServer(service.Connect())
	defer server.Close()
	token, err := security.GenerateSecureToken()
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateBrowserSyncTicket(context.Background(), "owner", a.VaultID, a.DeviceID, security.HashToken(token), session); err != nil {
		t.Fatal(err)
	}
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?ticket="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	challenge := socketTestRead(t, c, "challenge")
	var nonce string
	_ = json.Unmarshal(challenge["challenge"], &nonce)
	if err = c.WriteJSON(map[string]any{"type": "authenticate", "after": 0, "signature": ed25519.Sign(keyA, syncConnectionProof(a.VaultID, a.DeviceID, nonce))}); err != nil {
		t.Fatal(err)
	}
	socketTestRead(t, c, "welcome")
	// Several revalidations pass while the session stays active; no reconnect.
	time.Sleep(500 * time.Millisecond)
	if presence, err := store.BrowserSyncPresence(context.Background(), "owner", a.VaultID); err != nil || len(presence) != 1 || !presence[0].Online {
		t.Fatal("active session's socket closed", presence, err)
	}
	if _, err := database.Conn.Exec(`DELETE FROM sessions WHERE token_hash=$1`, session); err != nil {
		t.Fatal(err)
	}
	err = socketTestClosed(t, c, 3*time.Second)
	var closed *websocket.CloseError
	if !errors.As(err, &closed) || closed.Code != websocket.CloseServiceRestart {
		t.Fatal("revoked session's socket did not close for reconnect", err)
	}
}
