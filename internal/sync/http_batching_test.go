package browsersync

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func batchingSocketDial(t *testing.T, database *db.Database, base string, g SyncDeviceGrant, key ed25519.PrivateKey, batching bool) *websocket.Conn {
	t.Helper()
	token, err := security.GenerateSecureToken()
	if err != nil {
		t.Fatal(err)
	}
	if err = NewStore(database.Conn).CreateBrowserSyncTicket(context.Background(), "owner", g.VaultID, g.DeviceID, security.HashToken(token)); err != nil {
		t.Fatal(err)
	}
	query := "?protocol=3&ticket=" + token
	if batching {
		query += "&batching=1"
	}
	c, _, err := websocket.DefaultDialer.Dial(base+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	challenge := socketTestRead(t, c, "challenge")
	var nonce string
	_ = json.Unmarshal(challenge["challenge"], &nonce)
	if err = c.WriteJSON(map[string]any{"type": "authenticate", "after": 0, "signature": ed25519.Sign(key, syncConnectionProof(g.VaultID, g.DeviceID, nonce))}); err != nil {
		t.Fatal(err)
	}
	socketTestRead(t, c, "welcome")
	return c
}

// quietOf reads a batch_policy frame's workspace quiet period.
func quietOf(t *testing.T, frame map[string]json.RawMessage) int64 {
	t.Helper()
	var policy struct {
		Lanes map[string]BatchLane `json:"lanes"`
	}
	if err := json.Unmarshal(frame["policy"], &policy); err != nil {
		t.Fatal(err)
	}
	return policy.Lanes["workspace"].QuietMs
}

// noFrame fails if a frame of kind arrives within d.
func noFrame(t *testing.T, c *websocket.Conn, kind string, d time.Duration) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(d))
	for {
		var value map[string]json.RawMessage
		if err := c.ReadJSON(&value); err != nil {
			return
		}
		var got string
		_ = json.Unmarshal(value["type"], &got)
		if got == kind {
			t.Fatalf("unexpected %s frame", kind)
		}
	}
}

func TestBrowserSyncBatchingPolicyIsTheBaseAndOnlyReachesBatchingClients(t *testing.T) {
	database, _ := browserSocketTestDatabase(t)
	store := NewStore(database.Conn)
	grant, key := socketTestVault(t, store)
	service := NewBrowserSyncService(database)
	mux := http.NewServeMux()
	mux.Handle("/ws", service.Connect())
	server := httptest.NewServer(mux)
	defer server.Close()
	base := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"

	// Older clients would reject the frame: they never get it.
	legacy := batchingSocketDial(t, database, base, grant, key, false)
	noFrame(t, legacy, "batch_policy", time.Second)
	legacy.Close()

	c := batchingSocketDial(t, database, base, grant, key, true)
	defer c.Close()
	// Every client gets the base timings; each device scales them by its own setting.
	if quiet := quietOf(t, socketTestRead(t, c, "batch_policy")); quiet != 1_000 {
		t.Fatalf("first policy = %d ms", quiet)
	}

	// Over budget: the op is turned away with a wait, and nothing is applied.
	for range syncPublishBurst {
		service.publishes.take(grant.DeviceID, time.Now())
	}
	op := map[string]any{"vault_id": grant.VaultID, "workspace_id": grant.DeviceID, "operation_id": uuid.NewString(), "device_id": grant.DeviceID}
	if err := c.WriteJSON(map[string]any{"type": "publish_workspace", "request_id": "r1", "workspace_op": op}); err != nil {
		t.Fatal(err)
	}
	limited := socketTestRead(t, c, "workspace_error")
	var code string
	var wait int64
	_ = json.Unmarshal(limited["code"], &code)
	_ = json.Unmarshal(limited["retry_after_ms"], &wait)
	if code != "sync_rate_limited" || wait < 1_000 {
		t.Fatalf("over budget: code=%s retry_after_ms=%d", code, wait)
	}
}
