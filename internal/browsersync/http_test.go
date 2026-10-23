package browsersync

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestSyncDecoderRejectsUnknownAndTrailingFields(t *testing.T) {
	for _, raw := range []string{`{"type":"heartbeat","password":"do not accept secrets"}`, `{"type":"heartbeat"}{}`, `{"type":"heartbeat","applied_sequence":1.5}`} {
		var target syncClientFrame
		if decodeSync(strings.NewReader(raw), &target) == nil {
			t.Fatal("invalid frame accepted")
		}
	}
}

func TestBrowserSyncWebSocketTwoServersReplayAndProof(t *testing.T) {
	database, peer := browserSocketTestDatabase(t)
	ctx := context.Background()
	root, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	a, keyA := socketTestGrant(uuid.NewString(), rootKey)
	b, keyB := socketTestGrant(a.VaultID, rootKey)
	wrapper := SyncKeyEnvelope{Version: 1, KDF: SyncKDFAccount, Salt: base64.StdEncoding.EncodeToString(make([]byte, 16)), Nonce: base64.StdEncoding.EncodeToString(make([]byte, 12)), Ciphertext: base64.StdEncoding.EncodeToString(make([]byte, 32))}
	if err := NewStore(database.Conn).CreateBrowserSyncVault(ctx, "owner", "", root, PasswordVaultKeys(wrapper), a); err != nil {
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
	presence, err := NewStore(database.Conn).BrowserSyncPresence(ctx, "owner", a.VaultID)
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
	w, err := NewStore(database.Conn).BrowserSyncVault(ctx, "owner")
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

func TestConnectionTimersAreSpread(t *testing.T) {
	seen := map[time.Duration]bool{}
	for i := 0; i < 50; i++ {
		d := jittered(10*time.Minute, 0.5)
		if d < 5*time.Minute || d > 15*time.Minute {
			t.Fatalf("reconnect outside its window: %v", d)
		}
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Fatal("every connection would reconnect at the same moment")
	}
}
