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

func workspaceSocketDial(t *testing.T, database *db.Database, base string, g SyncDeviceGrant, key ed25519.PrivateKey) *websocket.Conn {
	t.Helper()
	token, err := security.GenerateSecureToken()
	if err != nil {
		t.Fatal(err)
	}
	if err = NewStore(database.Conn).CreateBrowserSyncTicket(context.Background(), "owner", g.VaultID, g.DeviceID, security.HashToken(token)); err != nil {
		t.Fatal(err)
	}
	c, _, err := websocket.DefaultDialer.Dial(base+"?protocol=3&ticket="+token, nil)
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

// The native client publishes only after a watched workspace's state arrives, so a
// watch must always answer, even for a brand-new (version 0) workspace.
func TestBrowserSyncWorkspaceSocketWatchClaimAndPublish(t *testing.T) {
	database, _ := browserSocketTestDatabase(t)
	ctx := context.Background()
	root, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	a, keyA := socketTestGrant(uuid.NewString(), rootKey)
	b, keyB := socketTestGrant(a.VaultID, rootKey)
	wrapper := SyncKeyEnvelope{Version: 1, KDF: "argon2id-m65536-t3-p1", Salt: base64.StdEncoding.EncodeToString(make([]byte, 16)), Nonce: base64.StdEncoding.EncodeToString(make([]byte, 12)), Ciphertext: base64.StdEncoding.EncodeToString(make([]byte, 32))}
	store := NewStore(database.Conn)
	if err := store.CreateBrowserSyncVault(ctx, "owner", root, wrapper, a); err != nil {
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

	ca := workspaceSocketDial(t, database, base, a, keyA)
	defer ca.Close()
	socketTestRead(t, ca, "workspaces")
	for _, workspace := range []string{a.VaultID, a.DeviceID} {
		if err := ca.WriteJSON(map[string]any{"type": "watch_workspace", "workspace_id": workspace, "after": 0}); err != nil {
			t.Fatal(err)
		}
		current := socketTestRead(t, ca, "workspace_current")
		var id string
		_ = json.Unmarshal(current["workspace_id"], &id)
		if id != workspace {
			t.Fatalf("workspace_current for %s, want %s", id, workspace)
		}
	}

	// B takes A's workspace over the socket; A sees the new driver in its roster.
	cb := workspaceSocketDial(t, database, base, b, keyB)
	defer cb.Close()
	claim := SyncWorkspaceClaim{VaultID: a.VaultID, WorkspaceID: a.DeviceID, OperationID: uuid.NewString(), DeviceID: b.DeviceID, DeviceCounter: 1, KeyEpoch: 1}
	claim.Signature = ed25519.Sign(keyB, claim.SigningBytes())
	if err := cb.WriteJSON(map[string]any{"type": "claim", "request_id": "r1", "claim": claim}); err != nil {
		t.Fatal(err)
	}
	ack := socketTestRead(t, cb, "workspace_ack")
	var receipt SyncReceipt
	if json.Unmarshal(ack["receipt"], &receipt) != nil || receipt.Discarded {
		t.Fatalf("claim not accepted: %s", ack["receipt"])
	}
	for range 5 {
		frame := socketTestRead(t, ca, "workspaces")
		var workspaces []SyncWorkspace
		_ = json.Unmarshal(frame["workspaces"], &workspaces)
		for _, workspace := range workspaces {
			if workspace.WorkspaceID == a.DeviceID && workspace.DriverDeviceID != nil && *workspace.DriverDeviceID == b.DeviceID {
				goto claimed
			}
		}
	}
	t.Fatal("A never saw B take its workspace")
claimed:
	// B publishes to the workspace it now drives; A's watch delivers the delta.
	op := SyncWorkspaceOp{VaultID: a.VaultID, WorkspaceID: a.DeviceID, OperationID: uuid.NewString(), DeviceID: b.DeviceID, DeviceCounter: 2, KeyEpoch: 1, MerkleRoot: make([]byte, 32), Upserts: []SyncNodeWrite{{NodeID: a.DeviceID, Ciphertext: make([]byte, 40)}}}
	op.Signature = ed25519.Sign(keyB, op.SigningBytes())
	if err := cb.WriteJSON(map[string]any{"type": "publish_workspace", "request_id": "r2", "workspace_op": op}); err != nil {
		t.Fatal(err)
	}
	ack = socketTestRead(t, cb, "workspace_ack")
	if json.Unmarshal(ack["receipt"], &receipt) != nil || receipt.Discarded || receipt.WorkspaceVersion != 1 {
		t.Fatalf("publish not accepted: %s", ack["receipt"])
	}
	socketTestRead(t, ca, "workspace_delta")
}
