package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"github.com/lib/pq"
)

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
	workspacesMigration, err := os.ReadFile(filepath.Join("..", "platform", "postgres", "migrations", "20270926000000_browser_sync_trees.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(strings.ReplaceAll(strings.Split(string(workspacesMigration), "-- +goose Down")[0], "public.", "")); err != nil {
		t.Fatal(err)
	}
	applyLaterMigrations(t, conn, filepath.Join("..", "platform", "postgres", "migrations"))
	return a, b
}

func socketTestGrant(vault string, root ed25519.PrivateKey) (SyncDeviceGrant, ed25519.PrivateKey) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	g := SyncDeviceGrant{VaultID: vault, DeviceID: uuid.NewString(), KeyEpoch: 1, PublicKey: pub}
	g.Signature = ed25519.Sign(root, g.SigningBytes())
	return g, key
}

func socketTestMutation(g SyncDeviceGrant, key ed25519.PrivateKey, counter int64) SyncMutation {
	m := SyncMutation{VaultID: g.VaultID, OperationID: uuid.NewString(), DeviceID: g.DeviceID, DeviceCounter: counter, KeyEpoch: 1, Envelope: SyncEnvelope{Version: 1, Nonce: base64.StdEncoding.EncodeToString(make([]byte, 12)), Ciphertext: base64.StdEncoding.EncodeToString(make([]byte, 32))}}
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
	if err = NewStore(database.Conn).CreateBrowserSyncTicket(context.Background(), "owner", g.VaultID, g.DeviceID, security.HashToken(token)); err != nil {
		t.Fatal(err)
	}
	c, _, err := websocket.DefaultDialer.Dial(url+"?ticket="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	challenge := socketTestRead(t, c, "challenge")
	var nonce string
	_ = json.Unmarshal(challenge["challenge"], &nonce)
	if err = c.WriteJSON(map[string]any{"type": "authenticate", "after": after, "signature": ed25519.Sign(key, syncConnectionProof(g.VaultID, g.DeviceID, nonce))}); err != nil {
		c.Close()
		t.Fatal(err)
	}
	return c, token
}
