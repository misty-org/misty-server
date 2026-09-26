package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserSyncControlRequestReachesSocketAndRequiresTargetSignature(t *testing.T) {
	database, peer := browserSocketTestDatabase(t)
	ctx := context.Background()
	root, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	grant, key := socketTestGrant(uuid.NewString(), rootKey)
	wrapper := SyncKeyEnvelope{Version: 1, KDF: "argon2id-m65536-t3-p1", Salt: base64.StdEncoding.EncodeToString(make([]byte, 16)), Nonce: base64.StdEncoding.EncodeToString(make([]byte, 12)), Ciphertext: base64.StdEncoding.EncodeToString(make([]byte, 32))}
	if err := NewStore(database.Conn).CreateBrowserSyncWorkspace(ctx, "owner", root, wrapper, grant); err != nil {
		t.Fatal(err)
	}
	version := 1
	if _, err := NewStore(database.Conn).ControlBrowserSyncDevice(ctx, "owner", SyncDeviceControl{DeviceID: grant.DeviceID, ControlVersion: &version}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewBrowserSyncService(peer).Connect())
	defer server.Close()
	socket, _ := socketTestDial(t, database, "ws"+strings.TrimPrefix(server.URL, "http"), grant, key, 0)
	defer socket.Close()
	socketTestRead(t, socket, "welcome")
	socketTestRead(t, socket, "devices")
	request, err := NewStore(database.Conn).ControlBrowserSyncDevice(ctx, "owner", SyncDeviceControl{DeviceID: grant.DeviceID, Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	frame := socketTestRead(t, socket, "devices")
	var devices []SyncDevice
	if err = json.Unmarshal(frame["devices"], &devices); err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].ActivationRequest == nil || *devices[0].ActivationRequest != request {
		t.Fatalf("missing control request: %+v", devices)
	}
	workspace, err := NewStore(database.Conn).BrowserSyncWorkspace(ctx, "owner")
	if err != nil || workspace.HeadSequence != 0 {
		t.Fatal("request changed workspace before target signed it")
	}
	activation := socketTestMutation(grant, key, 1)
	activation.OperationID = request
	activation.Signature = ed25519.Sign(key, activation.SigningBytes())
	if err = socket.WriteJSON(map[string]any{"type": "heartbeat", "applied_sequence": 0, "ready": true, "activation": activation}); err != nil {
		t.Fatal(err)
	}
	ack := socketTestRead(t, socket, "ack")
	var receipt SyncReceipt
	if err = json.Unmarshal(ack["receipt"], &receipt); err != nil || receipt.Sequence != 1 || receipt.Discarded {
		t.Fatalf("activation rejected: %+v %v", receipt, err)
	}
	presence, err := NewStore(database.Conn).BrowserSyncPresence(ctx, "owner", grant.WorkspaceID)
	if err != nil || len(presence) != 1 || !presence[0].Active {
		t.Fatalf("active device not confirmed: %+v %v", presence, err)
	}
	disabled := false
	if _, err = NewStore(database.Conn).ControlBrowserSyncDevice(ctx, "owner", SyncDeviceControl{DeviceID: grant.DeviceID, FullSync: &disabled}); err != nil {
		t.Fatal(err)
	}
	// Activation may already have queued a presence snapshot before this update.
	// The socket must converge to the committed policy without reconnecting.
	for attempt := 0; attempt < 10; attempt++ {
		frame = socketTestRead(t, socket, "devices")
		if err = json.Unmarshal(frame["devices"], &devices); err != nil {
			t.Fatal(err)
		}
		if len(devices) == 1 && !devices[0].FullSync {
			break
		}
	}
	if len(devices) != 1 || devices[0].FullSync {
		t.Fatal("independent policy did not reach connected device")
	}
	presence, err = NewStore(database.Conn).BrowserSyncPresence(ctx, "owner", grant.WorkspaceID)
	if err != nil || !presence[0].Online {
		t.Fatal("turning Full sync off disconnected presence")
	}
}
