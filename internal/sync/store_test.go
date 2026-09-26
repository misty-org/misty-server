package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/kannachi323/misty/server/internal/platform/transport"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

func syncTestEnvelope() SyncEnvelope {
	return SyncEnvelope{1, base64.StdEncoding.EncodeToString(make([]byte, 12)), base64.StdEncoding.EncodeToString(make([]byte, 32))}
}
func syncTestKeyEnvelope() SyncKeyEnvelope {
	e := syncTestEnvelope()
	return SyncKeyEnvelope{e.Version, "argon2id-m65536-t3-p1", base64.StdEncoding.EncodeToString(make([]byte, 16)), e.Nonce, e.Ciphertext}
}
func syncTestGrant(workspace string, root ed25519.PrivateKey) (SyncDeviceGrant, ed25519.PrivateKey) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	g := SyncDeviceGrant{WorkspaceID: workspace, DeviceID: uuid.NewString(), KeyEpoch: 1, PublicKey: pub}
	g.Signature = ed25519.Sign(root, g.SigningBytes())
	return g, key
}
func syncTestMutation(g SyncDeviceGrant, key ed25519.PrivateKey, counter int64) SyncMutation {
	m := SyncMutation{WorkspaceID: g.WorkspaceID, OperationID: uuid.NewString(), DeviceID: g.DeviceID, DeviceCounter: counter, KeyEpoch: 1, Envelope: syncTestEnvelope()}
	m.Signature = ed25519.Sign(key, m.SigningBytes())
	return m
}

func TestBrowserSyncEnvelopeBoundary(t *testing.T) {
	if !syncTestEnvelope().Validate(SyncMaxEventBytes) || !syncTestKeyEnvelope().Valid() {
		t.Fatal("valid envelopes rejected")
	}
	for _, change := range []func(*SyncEnvelope){func(e *SyncEnvelope) { e.Nonce = "bad" }, func(e *SyncEnvelope) { e.Version = 2 }, func(e *SyncEnvelope) { e.Ciphertext = "plaintext" }, func(e *SyncEnvelope) { e.Ciphertext = base64.StdEncoding.EncodeToString(make([]byte, 15)) }} {
		e := syncTestEnvelope()
		change(&e)
		if e.Validate(SyncMaxEventBytes) {
			t.Fatal("invalid envelope accepted")
		}
	}
	g, key := syncTestGrant(uuid.NewString(), make(ed25519.PrivateKey, 64))
	m := syncTestMutation(g, key, 1)
	if !m.Valid() {
		t.Fatal("valid mutation rejected")
	}
	m.DeviceCounter = SyncMaxCounter + 1
	if m.Valid() {
		t.Fatal("unsafe counter accepted")
	}
	m.DeviceCounter = 1
	m.OperationID = strings.ToUpper(uuid.NewString())
	if m.Valid() {
		t.Fatal("noncanonical identifier accepted")
	}
}

func syncTestDatabase(t *testing.T) (*Store, string) {
	t.Helper()
	dsn := os.Getenv("MISTY_BROWSER_SYNC_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable misty_browser_sync_test PostgreSQL")
	}
	base, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err = base.QueryRow("SELECT current_database()").Scan(&name); err != nil {
		base.Close()
		t.Fatal(err)
	}
	if name != "misty_browser_sync_test" {
		base.Close()
		t.Fatal("refusing non-disposable database")
	}
	schema := "sync_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema)); err != nil {
		base.Close()
		t.Fatal(err)
	}
	scoped := dsn + " search_path=" + schema
	conn, err := sql.Open("postgres", scoped)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close()
		base.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		base.Close()
	})
	if _, err = conn.Exec(`CREATE TABLE users(id text primary key); INSERT INTO users VALUES('owner'),('other')`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../test/fixtures/schema-history/20270212120000_browser_sync.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	activeMigration, err := os.ReadFile("../../test/fixtures/schema-history/20270214120000_browser_sync_active_device.sql")
	if err != nil {
		t.Fatal(err)
	}
	controlsMigration, controlsErr := os.ReadFile("../../test/fixtures/schema-history/20270215120000_browser_sync_controls.sql")
	if controlsErr != nil {
		t.Fatal(controlsErr)
	}
	if _, err = conn.Exec(strings.Split(string(activeMigration), "-- +goose Down")[0] + "\n" + strings.Split(string(controlsMigration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	// The tree migration is schema-qualified for production; tests run it in
	// their disposable schema.
	treesMigration, err := os.ReadFile("../platform/postgres/migrations/20270926000000_browser_sync_trees.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(strings.ReplaceAll(strings.Split(string(treesMigration), "-- +goose Down")[0], "public.", "")); err != nil {
		t.Fatal(err)
	}
	return &Store{Conn: conn}, scoped
}

func TestBrowserSyncPostgresMultiwriterRecovery(t *testing.T) {
	database, dsn := syncTestDatabase(t)
	ctx := context.Background()
	root, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	a, keyA := syncTestGrant(uuid.NewString(), rootKey)
	if err := database.CreateBrowserSyncWorkspace(ctx, "owner", root, syncTestKeyEnvelope(), a); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateBrowserSyncWorkspace(ctx, "owner", root, syncTestKeyEnvelope(), a); !errors.Is(err, ErrSyncExists) {
		t.Fatalf("bootstrap overwrite: %v", err)
	}
	b, keyB := syncTestGrant(a.WorkspaceID, rootKey)
	if err := database.EnrollBrowserSyncDevice(ctx, "other", b); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("cross-account enrollment: %v", err)
	}
	if err := database.EnrollBrowserSyncDevice(ctx, "owner", b); err != nil {
		t.Fatal(err)
	}
	if err := database.EnrollBrowserSyncDevice(ctx, "owner", b); err != nil {
		t.Fatalf("enrollment retry: %v", err)
	}
	replacement, _ := syncTestGrant(a.WorkspaceID, rootKey)
	replacement.DeviceID = b.DeviceID
	replacement.Signature = ed25519.Sign(rootKey, replacement.SigningBytes())
	if err := database.EnrollBrowserSyncDevice(ctx, "owner", replacement); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("changed device key accepted: %v", err)
	}

	listener := pq.NewListener(dsn, time.Millisecond*10, time.Second, nil)
	defer listener.Close()
	if err := listener.Listen("misty_account_events"); err != nil {
		t.Fatal(err)
	}
	mutations := []SyncMutation{syncTestMutation(a, keyA, 1), syncTestMutation(b, keyB, 1)}
	receipts := make([]*SyncReceipt, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range mutations {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			receipts[i], errs[i] = database.PublishBrowserSync(ctx, "owner", mutations[i])
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if receipts[0].Sequence == receipts[1].Sequence || receipts[0].Sequence+receipts[1].Sequence != 3 {
		t.Fatal("concurrent writes did not receive distinct contiguous sequences")
	}
	// A server-to-server listener receives only committed metadata, never ciphertext.
	for range mutations {
		select {
		case n := <-listener.Notify:
			if n == nil {
				t.Fatal("unexpected reconnect")
			}
			var e transport.AccountEvent
			if json.Unmarshal([]byte(n.Extra), &e) != nil || e.UserID != "owner" || e.Topic != "browser-sync" || e.ID != a.WorkspaceID || strings.Contains(n.Extra, "ciphertext") {
				t.Fatalf("bad notification: %s", n.Extra)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("missing committed notification")
		}
	}
	for i, m := range mutations {
		r, err := database.PublishBrowserSync(ctx, "owner", m)
		if err != nil || r.Sequence != receipts[i].Sequence {
			t.Fatalf("lost acknowledgment retry changed receipt: %+v %v", r, err)
		}
	}
	changed := mutations[0]
	changed.Envelope.Ciphertext = base64.StdEncoding.EncodeToString(make([]byte, 40))
	changed.Signature = ed25519.Sign(keyA, changed.SigningBytes())
	if _, err := database.PublishBrowserSync(ctx, "owner", changed); !errors.Is(err, ErrSyncOperationConflict) {
		t.Fatalf("operation mutation accepted: %v", err)
	}
	if _, err := database.PublishBrowserSync(ctx, "owner", syncTestMutation(a, keyA, 3)); !errors.Is(err, ErrSyncCounterGap) {
		t.Fatalf("out-of-order counter accepted: %v", err)
	}
	if _, err := database.PublishBrowserSync(ctx, "other", mutations[0]); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("cross-account publication: %v", err)
	}
	tampered := syncTestMutation(a, keyA, 2)
	tampered.Envelope.Ciphertext = changed.Envelope.Ciphertext
	if _, err := database.PublishBrowserSync(ctx, "owner", tampered); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("tampered signature accepted: %v", err)
	}
	oldEpoch := syncTestMutation(a, keyA, 2)
	oldEpoch.KeyEpoch = 2
	oldEpoch.Signature = ed25519.Sign(keyA, oldEpoch.SigningBytes())
	if _, err := database.PublishBrowserSync(ctx, "owner", oldEpoch); !errors.Is(err, ErrSyncEpoch) {
		t.Fatalf("wrong epoch accepted: %v", err)
	}
	replay, err := database.ReplayBrowserSync(ctx, "owner", a.WorkspaceID, a.DeviceID, 0, 200)
	if err != nil || replay.HeadSequence != 2 || replay.CheckpointRequired || len(replay.Events) != 2 {
		t.Fatalf("bad replay: %+v %v", replay, err)
	}
	if _, err = database.ReplayBrowserSync(ctx, "other", a.WorkspaceID, a.DeviceID, 0, 200); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("cross-account replay: %v", err)
	}
	if _, err = database.ReplayBrowserSync(ctx, "owner", a.WorkspaceID, a.DeviceID, 3, 200); !errors.Is(err, ErrSyncCursor) {
		t.Fatalf("future cursor accepted: %v", err)
	}
	// Force a mid-transaction failure after the event insert to verify atomicity.
	if _, err = database.Conn.Exec(`ALTER TABLE browser_sync_receipts ADD CONSTRAINT test_abort CHECK(sequence <> 3)`); err != nil {
		t.Fatal(err)
	}
	next := syncTestMutation(a, keyA, 2)
	if _, err = database.PublishBrowserSync(ctx, "owner", next); err == nil {
		t.Fatal("failure injection did not abort")
	}
	w, err := database.BrowserSyncWorkspace(ctx, "owner")
	if err != nil || w.HeadSequence != 2 {
		t.Fatal("failed commit advanced head")
	}
	var count int
	if err = database.Conn.QueryRow(`SELECT count(*) FROM browser_sync_events WHERE sequence=3`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed transaction leaked event")
	}
	select {
	case <-listener.Notify:
		t.Fatal("retry/failure produced an uncommitted notification")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err = database.Conn.Exec(`ALTER TABLE browser_sync_receipts DROP CONSTRAINT test_abort`); err != nil {
		t.Fatal(err)
	}
	r, err := database.PublishBrowserSync(ctx, "owner", next)
	if err != nil || r.Sequence != 3 {
		t.Fatalf("recovery after abort: %+v %v", r, err)
	}
	if _, err = database.Conn.Exec(`UPDATE browser_sync_devices SET revoked_at=now() WHERE device_id=$1`, b.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err = database.PublishBrowserSync(ctx, "owner", syncTestMutation(b, keyB, 2)); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("revoked writer accepted: %v", err)
	}
	if _, err = database.ReplayBrowserSync(ctx, "owner", a.WorkspaceID, b.DeviceID, 0, 200); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("revoked reader accepted: %v", err)
	}
	if err = database.EnrollBrowserSyncDevice(ctx, "owner", b); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("revoked enrollment replay accepted: %v", err)
	}
	if _, err = database.Conn.Exec(`DELETE FROM browser_sync_receipts WHERE operation_id=$1;`, mutations[0].OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err = database.PublishBrowserSync(ctx, "owner", mutations[0]); !errors.Is(err, ErrSyncCompacted) {
		t.Fatalf("receipt compaction resurrected operation: %v", err)
	}
	if _, err = database.Conn.Exec(`DELETE FROM browser_sync_events WHERE sequence=1`); err != nil {
		t.Fatal(err)
	}
	replay, err = database.ReplayBrowserSync(ctx, "owner", a.WorkspaceID, a.DeviceID, 0, 200)
	if err != nil || !replay.CheckpointRequired || len(replay.Events) != 0 {
		t.Fatalf("compaction gap was not detected: %+v %v", replay, err)
	}
}

func TestBrowserSyncActiveHeartbeatHandoff(t *testing.T) {
	database, _ := syncTestDatabase(t)
	ctx := context.Background()
	root, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	a, keyA := syncTestGrant(uuid.NewString(), rootKey)
	if err := database.CreateBrowserSyncWorkspace(ctx, "owner", root, syncTestKeyEnvelope(), a); err != nil {
		t.Fatal(err)
	}
	b, keyB := syncTestGrant(a.WorkspaceID, rootKey)
	if err := database.EnrollBrowserSyncDevice(ctx, "owner", b); err != nil {
		t.Fatal(err)
	}
	publish := func(m SyncMutation, options SyncPublishOptions, sequence int64, discarded bool) {
		t.Helper()
		receipt, err := database.PublishBrowserSync(ctx, "owner", m, options)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Sequence != sequence || receipt.Discarded != discarded {
			t.Fatalf("unexpected receipt: %+v", receipt)
		}
	}
	claimA := syncTestMutation(a, keyA, 1)
	claimB := syncTestMutation(b, keyB, 1)
	publish(claimA, SyncPublishOptions{Activate: true}, 1, false)
	publish(claimB, SyncPublishOptions{Activate: true}, 2, false)
	// A delayed renewal and retried claim from A must not steal back control.
	identity := SyncConnectionIdentity{UserID: "owner", WorkspaceID: a.WorkspaceID, DeviceID: a.DeviceID}
	if err := database.BrowserSyncHeartbeat(ctx, identity, uuid.NewString(), 2, true, claimA.OperationID); err != nil {
		t.Fatal(err)
	}
	publish(claimA, SyncPublishOptions{Activate: true}, 1, false)
	stale := syncTestMutation(a, keyA, 2)
	publish(stale, SyncPublishOptions{ActiveEpoch: claimA.OperationID}, 2, true)
	publish(stale, SyncPublishOptions{ActiveEpoch: claimA.OperationID}, 2, true)
	publish(syncTestMutation(b, keyB, 2), SyncPublishOptions{ActiveEpoch: claimB.OperationID}, 3, false)
	// The discarded counter is consumed; a later explicit click still works.
	nextClaim := syncTestMutation(a, keyA, 3)
	publish(nextClaim, SyncPublishOptions{Activate: true}, 4, false)
	publish(syncTestMutation(a, keyA, 4), SyncPublishOptions{ActiveEpoch: claimA.OperationID}, 4, true)
	publish(syncTestMutation(a, keyA, 5), SyncPublishOptions{ActiveEpoch: nextClaim.OperationID}, 5, false)
	var count int
	if err := database.Conn.QueryRow("SELECT count(*) FROM browser_sync_events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("stale updates entered replay: %d events", count)
	}
	presence, err := database.BrowserSyncPresence(ctx, "owner", a.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range presence {
		if p.Active != (p.DeviceID == a.DeviceID) {
			t.Fatal("presence owner disagrees")
		}
	}
}
