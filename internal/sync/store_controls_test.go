package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestBrowserSyncDeviceControls(t *testing.T) {
	database, _ := syncTestDatabase(t)
	ctx := context.Background()
	root, key, _ := ed25519.GenerateKey(rand.Reader)
	grant, signingKey := syncTestGrant(uuid.NewString(), key)
	if err := database.CreateBrowserSyncWorkspace(ctx, "owner", root, syncTestKeyEnvelope(), grant); err != nil {
		t.Fatal(err)
	}
	enabled, disabled, version, name := true, false, 1, "Office Mac"
	control := SyncDeviceControl{DeviceID: grant.DeviceID, FullSync: &disabled}
	if _, err := database.ControlBrowserSyncDevice(ctx, "other", control); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("other account: %v", err)
	}
	if _, err := database.ControlBrowserSyncDevice(ctx, "owner", control); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("old client: %v", err)
	}
	if _, err := database.ControlBrowserSyncDevice(ctx, "owner", SyncDeviceControl{DeviceID: grant.DeviceID, ControlVersion: &version, DisplayName: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ControlBrowserSyncDevice(ctx, "owner", control); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("offline: %v", err)
	}
	identity := SyncConnectionIdentity{UserID: "owner", WorkspaceID: grant.WorkspaceID, DeviceID: grant.DeviceID}
	connection := uuid.NewString()
	if err := database.BrowserSyncHeartbeat(ctx, identity, connection, 0, true); err != nil {
		t.Fatal(err)
	}
	request, err := database.ControlBrowserSyncDevice(ctx, "owner", SyncDeviceControl{DeviceID: grant.DeviceID, Activate: true})
	if err != nil || !validSyncID(request) {
		t.Fatalf("activation request: %q %v", request, err)
	}
	devices, err := database.BrowserSyncDevices(ctx, "owner", grant.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	d := devices[0]
	if d.DisplayName != name || d.ActivationRequest == nil || *d.ActivationRequest != request || d.ActivationExpiresAt <= time.Now().UnixMilli() || d.ActivationExpiresAt > time.Now().Add(31*time.Second).UnixMilli() {
		t.Fatalf("bad roster: %+v", d)
	}
	workspace, err := database.BrowserSyncWorkspace(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if workspace.HeadSequence != 0 {
		t.Fatal("a request must not itself publish an activation")
	}
	if _, err := database.Conn.Exec(`UPDATE browser_sync_control_requests SET expires_at=clock_timestamp()-interval '1 second' WHERE operation_id=$1`, request); err != nil {
		t.Fatal(err)
	}
	mutation := syncTestMutation(grant, signingKey, 1)
	mutation.OperationID = request
	mutation.Signature = ed25519.Sign(signingKey, mutation.SigningBytes())
	receipt, err := database.PublishBrowserSync(ctx, "owner", mutation, SyncPublishOptions{Activate: true})
	if err != nil || !receipt.Discarded {
		t.Fatalf("expired request must consume counter without activation: %+v %v", receipt, err)
	}
	if _, err := database.ControlBrowserSyncDevice(ctx, "owner", control); err != nil {
		t.Fatal(err)
	}
	devices, err = database.BrowserSyncDevices(ctx, "owner", grant.WorkspaceID)
	if err != nil || devices[0].FullSync || devices[0].ActivationRequest != nil {
		t.Fatalf("policy/cancel failed: %+v %v", devices, err)
	}
	if _, err := database.ControlBrowserSyncDevice(ctx, "owner", SyncDeviceControl{DeviceID: grant.DeviceID, Activate: true}); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("independent device activated: %v", err)
	}
	if _, err := database.ControlBrowserSyncDevice(ctx, "owner", SyncDeviceControl{DeviceID: grant.DeviceID, FullSync: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn.Exec(`UPDATE browser_sync_devices SET revoked_at=clock_timestamp() WHERE device_id=$1`, grant.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ControlBrowserSyncDevice(ctx, "owner", control); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("revoked: %v", err)
	}
}
