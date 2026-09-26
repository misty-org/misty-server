package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func treeSocketDial(t *testing.T, database *db.Database, base string, g SyncDeviceGrant, key ed25519.PrivateKey) *websocket.Conn {
	t.Helper()
	token, err := security.GenerateSecureToken()
	if err != nil {
		t.Fatal(err)
	}
	if err = NewStore(database.Conn).CreateBrowserSyncTicket(context.Background(), "owner", g.WorkspaceID, g.DeviceID, security.HashToken(token)); err != nil {
		t.Fatal(err)
	}
	c, _, err := websocket.DefaultDialer.Dial(base+"?protocol=2&ticket="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	challenge := socketTestRead(t, c, "challenge")
	var nonce string
	_ = json.Unmarshal(challenge["challenge"], &nonce)
	if err = c.WriteJSON(map[string]any{"type": "authenticate", "after": 0, "signature": ed25519.Sign(key, syncConnectionProof(g.WorkspaceID, g.DeviceID, nonce))}); err != nil {
		t.Fatal(err)
	}
	socketTestRead(t, c, "welcome")
	return c
}

// The native client publishes only after a watched tree's state arrives, so a
// watch must always answer, even for a brand-new (version 0) tree.
func TestBrowserSyncTreeSocketWatchClaimAndPublish(t *testing.T) {
	database, _ := browserSocketTestDatabase(t)
	ctx := context.Background()
	root, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	a, keyA := socketTestGrant(uuid.NewString(), rootKey)
	b, keyB := socketTestGrant(a.WorkspaceID, rootKey)
	wrapper := SyncKeyEnvelope{Version: 1, KDF: "argon2id-m65536-t3-p1", Salt: base64.StdEncoding.EncodeToString(make([]byte, 16)), Nonce: base64.StdEncoding.EncodeToString(make([]byte, 12)), Ciphertext: base64.StdEncoding.EncodeToString(make([]byte, 32))}
	store := NewStore(database.Conn)
	if err := store.CreateBrowserSyncWorkspace(ctx, "owner", root, wrapper, a); err != nil {
		t.Fatal(err)
	}
	if err := store.EnrollBrowserSyncDevice(ctx, "owner", b); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/ws", NewBrowserSyncService(database).Connect())
	server := httptest.NewServer(mux)
	defer server.Close()
	base := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"

	ca := treeSocketDial(t, database, base, a, keyA)
	defer ca.Close()
	socketTestRead(t, ca, "trees")
	for _, tree := range []string{a.WorkspaceID, a.DeviceID} {
		if err := ca.WriteJSON(map[string]any{"type": "watch_tree", "tree_id": tree, "after": 0}); err != nil {
			t.Fatal(err)
		}
		current := socketTestRead(t, ca, "tree_current")
		var id string
		_ = json.Unmarshal(current["tree_id"], &id)
		if id != tree {
			t.Fatalf("tree_current for %s, want %s", id, tree)
		}
	}

	// B takes A's tree over the socket; A sees the new driver in its roster.
	cb := treeSocketDial(t, database, base, b, keyB)
	defer cb.Close()
	claim := SyncTreeClaim{WorkspaceID: a.WorkspaceID, TreeID: a.DeviceID, OperationID: uuid.NewString(), DeviceID: b.DeviceID, DeviceCounter: 1, KeyEpoch: 1}
	claim.Signature = ed25519.Sign(keyB, claim.SigningBytes())
	if err := cb.WriteJSON(map[string]any{"type": "claim", "request_id": "r1", "claim": claim}); err != nil {
		t.Fatal(err)
	}
	ack := socketTestRead(t, cb, "tree_ack")
	var receipt SyncReceipt
	if json.Unmarshal(ack["receipt"], &receipt) != nil || receipt.Discarded {
		t.Fatalf("claim not accepted: %s", ack["receipt"])
	}
	for range 5 {
		frame := socketTestRead(t, ca, "trees")
		var trees []SyncTree
		_ = json.Unmarshal(frame["trees"], &trees)
		for _, tree := range trees {
			if tree.TreeID == a.DeviceID && tree.DriverDeviceID != nil && *tree.DriverDeviceID == b.DeviceID {
				goto claimed
			}
		}
	}
	t.Fatal("A never saw B take its tree")
claimed:
	// B publishes to the tree it now drives; A's watch delivers the delta.
	op := SyncTreeOp{WorkspaceID: a.WorkspaceID, TreeID: a.DeviceID, OperationID: uuid.NewString(), DeviceID: b.DeviceID, DeviceCounter: 2, KeyEpoch: 1, MerkleRoot: make([]byte, 32), Upserts: []SyncNodeWrite{{NodeID: a.DeviceID, Ciphertext: make([]byte, 40)}}}
	op.Signature = ed25519.Sign(keyB, op.SigningBytes())
	if err := cb.WriteJSON(map[string]any{"type": "publish_tree", "request_id": "r2", "tree_op": op}); err != nil {
		t.Fatal(err)
	}
	ack = socketTestRead(t, cb, "tree_ack")
	if json.Unmarshal(ack["receipt"], &receipt) != nil || receipt.Discarded || receipt.TreeVersion != 1 {
		t.Fatalf("publish not accepted: %s", ack["receipt"])
	}
	socketTestRead(t, ca, "tree_delta")
}
