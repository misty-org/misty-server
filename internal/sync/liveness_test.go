package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/platform/transport"
	"github.com/lib/pq"
)

func livenessPresence(t *testing.T, store *Store, vault, device string) SyncPresence {
	t.Helper()
	presence, err := store.BrowserSyncPresence(context.Background(), "owner", vault)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range presence {
		if p.DeviceID == device {
			return p
		}
	}
	t.Fatal("device missing from presence")
	return SyncPresence{}
}

func livenessHint(t *testing.T, listener *pq.Listener, vault string) {
	t.Helper()
	select {
	case n := <-listener.Notify:
		var e transport.AccountEvent
		if n == nil || json.Unmarshal([]byte(n.Extra), &e) != nil || e.Topic != "browser-presence" || e.ID != vault {
			t.Fatal("unexpected notification", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing presence hint")
	}
}

func livenessQuiet(t *testing.T, listener *pq.Listener) {
	t.Helper()
	select {
	case n := <-listener.Notify:
		t.Fatal("unexpected notification", n)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBrowserSyncLivenessWritesOnlyTransitions(t *testing.T) {
	store, dsn := syncTestDatabase(t)
	ctx := context.Background()
	root, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	a, keyA := syncTestGrant(uuid.NewString(), rootKey)
	if err := store.CreateBrowserSyncVault(ctx, "owner", root, syncTestKeyEnvelope(), a); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishBrowserSync(ctx, "owner", syncTestMutation(a, keyA, 1)); err != nil {
		t.Fatal(err)
	}
	listener := pq.NewListener(dsn, 10*time.Millisecond, time.Second, nil)
	defer listener.Close()
	if err := listener.Listen("misty_account_events"); err != nil {
		t.Fatal(err)
	}
	identity := SyncConnectionIdentity{UserID: "owner", VaultID: a.VaultID, DeviceID: a.DeviceID}
	instance, connection := uuid.NewString(), uuid.NewString()
	if err := store.BrowserSyncConnect(ctx, identity, connection, instance, 0, false); err != nil {
		t.Fatal(err)
	}
	livenessHint(t, listener, a.VaultID)
	if p := livenessPresence(t, store, a.VaultID, a.DeviceID); !p.Online || p.Ready {
		t.Fatal("connected device", p)
	}
	// Progress that does not change readiness writes the cursor silently.
	if current, err := store.BrowserSyncProgress(ctx, identity, connection, 0, true, false); err != nil || current {
		t.Fatal("ready but behind head", current, err)
	}
	livenessQuiet(t, listener)
	if current, err := store.BrowserSyncProgress(ctx, identity, connection, 1, true, false); err != nil || !current {
		t.Fatal("caught up", current, err)
	}
	livenessHint(t, listener, a.VaultID)
	if p := livenessPresence(t, store, a.VaultID, a.DeviceID); !p.Ready || p.AppliedSequence != 1 {
		t.Fatal("caught-up presence", p)
	}
	// A cursor past the head is refused rather than recorded.
	if _, err := store.BrowserSyncProgress(ctx, identity, connection, 5, true, true); err == nil {
		t.Fatal("cursor beyond head accepted")
	}
	var rows int
	if err := store.Conn.QueryRow(`SELECT count(*) FROM browser_sync_connections`).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("open connection rows", rows, err)
	}
	if err := store.BrowserSyncDisconnect(ctx, identity, connection); err != nil {
		t.Fatal(err)
	}
	livenessHint(t, listener, a.VaultID)
	if err := store.Conn.QueryRow(`SELECT count(*) FROM browser_sync_connections`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("closed connection kept its row", rows, err)
	}
	if p := livenessPresence(t, store, a.VaultID, a.DeviceID); p.Online || p.LastSeenAt == nil {
		t.Fatal("disconnected device keeps no last-seen time", p)
	}
}
