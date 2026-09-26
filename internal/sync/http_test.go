package browsersync

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"github.com/lib/pq"
)

func TestSyncDecoderRejectsUnknownAndTrailingFields(t *testing.T) {
	for _, raw := range []string{`{"type":"heartbeat","password":"do not accept secrets"}`, `{"type":"heartbeat"}{}`, `{"type":"heartbeat","applied_sequence":1.5}`} {
		var target syncClientFrame
		if decodeSync(strings.NewReader(raw), &target) == nil {
			t.Fatal("invalid frame accepted")
		}
	}
}

func browserSocketTestDatabase(t *testing.T) (*db.Database, *db.Database) {
	t.Helper()
	dsn := os.Getenv("MISTY_BROWSER_SYNC_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable misty_browser_sync_test PostgreSQL")
	}
	base, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var name, user, port, socket string
	if err = base.QueryRow(`SELECT current_database(),current_user,current_setting('port'),current_setting('unix_socket_directories')`).Scan(&name, &user, &port, &socket); err != nil {
		base.Close()
		t.Fatal(err)
	}
	if name != "misty_browser_sync_test" || !strings.HasPrefix(socket, "/tmp/misty-browser-sync-pg.") {
		base.Close()
		t.Fatal("refusing non-disposable database")
	}
	t.Setenv("DB_HOST", socket)
	t.Setenv("DB_PORT", port)
	t.Setenv("DB_USER", user)
	t.Setenv("DB_NAME", name)
	t.Setenv("DB_PASSWORD", "")
	schema := "socket_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("postgres", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := sql.Open("postgres", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	a, b := &db.Database{Conn: conn}, &db.Database{Conn: peer}
	t.Cleanup(func() {
		a.Stop()
		b.Stop()
		base.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		base.Close()
	})
	if _, err = conn.Exec(`CREATE TABLE users(id text primary key); INSERT INTO users VALUES('owner'),('other')`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "test", "fixtures", "schema-history", "20270212120000_browser_sync.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	activeMigration, err := os.ReadFile(filepath.Join("..", "..", "test", "fixtures", "schema-history", "20270214120000_browser_sync_active_device.sql"))
	if err != nil {
		t.Fatal(err)
	}
	controlsMigration, err := os.ReadFile(filepath.Join("..", "..", "test", "fixtures", "schema-history", "20270215120000_browser_sync_controls.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(strings.Split(string(activeMigration), "-- +goose Down")[0] + "\n" + strings.Split(string(controlsMigration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	treesMigration, err := os.ReadFile(filepath.Join("..", "platform", "postgres", "migrations", "20270926000000_browser_sync_trees.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(strings.ReplaceAll(strings.Split(string(treesMigration), "-- +goose Down")[0], "public.", "")); err != nil {
		t.Fatal(err)
	}
	return a, b
}
func socketTestGrant(workspace string, root ed25519.PrivateKey) (SyncDeviceGrant, ed25519.PrivateKey) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	g := SyncDeviceGrant{WorkspaceID: workspace, DeviceID: uuid.NewString(), KeyEpoch: 1, PublicKey: pub}
	g.Signature = ed25519.Sign(root, g.SigningBytes())
	return g, key
}
func socketTestMutation(g SyncDeviceGrant, key ed25519.PrivateKey, counter int64) SyncMutation {
	m := SyncMutation{WorkspaceID: g.WorkspaceID, OperationID: uuid.NewString(), DeviceID: g.DeviceID, DeviceCounter: counter, KeyEpoch: 1, Envelope: SyncEnvelope{Version: 1, Nonce: base64.StdEncoding.EncodeToString(make([]byte, 12)), Ciphertext: base64.StdEncoding.EncodeToString(make([]byte, 32))}}
	m.Signature = ed25519.Sign(key, m.SigningBytes())
	return m
}
func socketTestRead(t *testing.T, c *websocket.Conn, want string) map[string]json.RawMessage {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(4 * time.Second))
	for range 30 {
		var value map[string]json.RawMessage
		if err := c.ReadJSON(&value); err != nil {
			t.Fatalf("waiting for %s: %v", want, err)
		}
		var kind string
		_ = json.Unmarshal(value["type"], &kind)
		if kind == want {
			return value
		}
	}
	t.Fatalf("did not receive %s", want)
	return nil
}
func socketTestDial(t *testing.T, database *db.Database, url string, g SyncDeviceGrant, key ed25519.PrivateKey, after int64) (*websocket.Conn, string) {
	t.Helper()
	token, err := security.GenerateSecureToken()
	if err != nil {
		t.Fatal(err)
	}
	if err = NewStore(database.Conn).CreateBrowserSyncTicket(context.Background(), "owner", g.WorkspaceID, g.DeviceID, security.HashToken(token)); err != nil {
		t.Fatal(err)
	}
	c, _, err := websocket.DefaultDialer.Dial(url+"?ticket="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	challenge := socketTestRead(t, c, "challenge")
	var nonce string
	_ = json.Unmarshal(challenge["challenge"], &nonce)
	if err = c.WriteJSON(map[string]any{"type": "authenticate", "after": after, "signature": ed25519.Sign(key, syncConnectionProof(g.WorkspaceID, g.DeviceID, nonce))}); err != nil {
		c.Close()
		t.Fatal(err)
	}
	return c, token
}

func TestBrowserSyncWebSocketTwoServersReplayAndProof(t *testing.T) {
	database, peer := browserSocketTestDatabase(t)
	ctx := context.Background()
	root, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	a, keyA := socketTestGrant(uuid.NewString(), rootKey)
	b, keyB := socketTestGrant(a.WorkspaceID, rootKey)
	wrapper := SyncKeyEnvelope{Version: 1, KDF: "argon2id-m65536-t3-p1", Salt: base64.StdEncoding.EncodeToString(make([]byte, 16)), Nonce: base64.StdEncoding.EncodeToString(make([]byte, 12)), Ciphertext: base64.StdEncoding.EncodeToString(make([]byte, 32))}
	if err := NewStore(database.Conn).CreateBrowserSyncWorkspace(ctx, "owner", root, wrapper, a); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(database.Conn).EnrollBrowserSyncDevice(ctx, "owner", b); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/one", NewBrowserSyncService(database).Connect())
	mux.Handle("/two", NewBrowserSyncService(peer).Connect())
	server := httptest.NewServer(mux)
	defer server.Close()
	base := "ws" + strings.TrimPrefix(server.URL, "http")
	ca, ticket := socketTestDial(t, database, base+"/one", a, keyA, 0)
	defer ca.Close()
	socketTestRead(t, ca, "welcome")
	cb, _ := socketTestDial(t, database, base+"/two", b, keyB, 0)
	defer cb.Close()
	socketTestRead(t, cb, "welcome")
	if reused, response, err := websocket.DefaultDialer.Dial(base+"/one?ticket="+ticket, nil); err == nil {
		reused.Close()
		t.Fatal("ticket reused")
	} else if response == nil || response.StatusCode != 403 {
		t.Fatal("invalid ticket status")
	}
	m1, m2 := socketTestMutation(a, keyA, 1), socketTestMutation(b, keyB, 1)
	if err := ca.WriteJSON(map[string]any{"type": "publish", "mutation": m1}); err != nil {
		t.Fatal(err)
	}
	ack := socketTestRead(t, ca, "ack")
	var receipt SyncReceipt
	if json.Unmarshal(ack["receipt"], &receipt) != nil || receipt.Sequence != 1 {
		t.Fatal("missing first acknowledgment")
	}
	live := socketTestRead(t, cb, "events")
	var replay SyncReplay
	if json.Unmarshal(live["replay"], &replay) != nil || len(replay.Events) != 1 || replay.Events[0].OperationID != m1.OperationID {
		t.Fatal("cross-server event was not delivered")
	}
	if err := cb.WriteJSON(map[string]any{"type": "publish", "mutation": m2}); err != nil {
		t.Fatal(err)
	}
	ack = socketTestRead(t, cb, "ack")
	if json.Unmarshal(ack["receipt"], &receipt) != nil || receipt.Sequence != 2 {
		t.Fatal("second writer was not accepted")
	}
	// A lost response may be retried without duplicating its durable event.
	if err := ca.WriteJSON(map[string]any{"type": "publish", "mutation": m1}); err != nil {
		t.Fatal(err)
	}
	ack = socketTestRead(t, ca, "ack")
	if json.Unmarshal(ack["receipt"], &receipt) != nil || receipt.Sequence != 1 {
		t.Fatal("retry got a new sequence")
	}
	fresh, _ := socketTestDial(t, database, base+"/one", a, keyA, 1)
	defer fresh.Close()
	socketTestRead(t, fresh, "welcome")
	live = socketTestRead(t, fresh, "events")
	if json.Unmarshal(live["replay"], &replay) != nil || len(replay.Events) != 1 || replay.Events[0].Sequence != 2 {
		t.Fatal("reconnect did not replay missing committed event")
	}
	// Disconnecting an older socket must not make its replacement disappear.
	ca.Close()
	presence, err := NewStore(database.Conn).BrowserSyncPresence(ctx, "owner", a.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	online := false
	for _, p := range presence {
		if p.DeviceID == a.DeviceID {
			online = p.Online
		}
	}
	if !online {
		t.Fatal("old connection removed replacement presence")
	}
	// A valid account ticket without the native device key cannot see welcome,
	// key wrappers, presence, or events.
	bad, _ := socketTestDial(t, database, base+"/one", a, keyB, 0)
	defer bad.Close()
	_ = bad.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := bad.ReadMessage(); err == nil {
		t.Fatal("wrong-key connection received private state")
	}
	// A peer cannot publish as the other device, even with a valid payload.
	if err := cb.WriteJSON(map[string]any{"type": "publish", "mutation": socketTestMutation(a, keyA, 2)}); err != nil {
		t.Fatal(err)
	}
	rejected := socketTestRead(t, cb, "error")
	if !bytes.Contains(rejected["code"], []byte("sync_device_forbidden")) {
		t.Fatal("identity mismatch accepted")
	}
	w, err := NewStore(database.Conn).BrowserSyncWorkspace(ctx, "owner")
	if err != nil || w.HeadSequence != 2 {
		t.Fatal("rejected frames changed durable head")
	}
	claim := socketTestMutation(b, keyB, 2)
	if err := cb.WriteJSON(map[string]any{"type": "heartbeat", "applied_sequence": 2, "ready": true, "activation": claim}); err != nil {
		t.Fatal(err)
	}
	ack = socketTestRead(t, cb, "ack")
	if json.Unmarshal(ack["receipt"], &receipt) != nil || receipt.Sequence != 3 || receipt.Discarded {
		t.Fatal("takeover heartbeat was not accepted")
	}
	live = socketTestRead(t, fresh, "events")
	if json.Unmarshal(live["replay"], &replay) != nil || len(replay.Events) != 1 || replay.Events[0].OperationID != claim.OperationID {
		t.Fatal("takeover did not reach follower")
	}
	stale := socketTestMutation(a, keyA, 2)
	if err := fresh.WriteJSON(map[string]any{"type": "publish", "mutation": stale}); err != nil {
		t.Fatal(err)
	}
	ack = socketTestRead(t, fresh, "ack")
	if json.Unmarshal(ack["receipt"], &receipt) != nil || receipt.Sequence != 3 || !receipt.Discarded {
		t.Fatal("follower write was not discarded")
	}
	if err := cb.WriteJSON(map[string]any{"type": "publish", "active_epoch": claim.OperationID, "mutation": socketTestMutation(b, keyB, 3)}); err != nil {
		t.Fatal(err)
	}
	ack = socketTestRead(t, cb, "ack")
	receipt = SyncReceipt{}
	if json.Unmarshal(ack["receipt"], &receipt) != nil || receipt.Sequence != 4 || receipt.Discarded {
		t.Fatal("active device could not publish")
	}
}
