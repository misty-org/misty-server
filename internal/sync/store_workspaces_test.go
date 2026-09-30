package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type workspaceTestDevice struct {
	grant   SyncDeviceGrant
	key     ed25519.PrivateKey
	counter int64
}

func workspaceCipher() []byte {
	out := make([]byte, 40)
	_, _ = rand.Read(out)
	return out
}

func (d *workspaceTestDevice) op(workspace string, base int64, upserts []SyncNodeWrite, slots []SyncSlotWrite, deletes []string) SyncWorkspaceOp {
	d.counter++
	op := SyncWorkspaceOp{VaultID: d.grant.VaultID, WorkspaceID: workspace, OperationID: uuid.NewString(), DeviceID: d.grant.DeviceID, DeviceCounter: d.counter, KeyEpoch: 1, BaseWorkspaceVersion: base, MerkleRoot: make([]byte, 32), Upserts: append([]SyncNodeWrite{{NodeID: workspace, Ciphertext: workspaceCipher()}}, upserts...), Slots: slots, Deletes: deletes}
	op.Signature = ed25519.Sign(d.key, op.SigningBytes())
	return op
}

func (d *workspaceTestDevice) claim(workspace, operation string) SyncWorkspaceClaim {
	d.counter++
	if operation == "" {
		operation = uuid.NewString()
	}
	c := SyncWorkspaceClaim{VaultID: d.grant.VaultID, WorkspaceID: workspace, OperationID: operation, DeviceID: d.grant.DeviceID, DeviceCounter: d.counter, KeyEpoch: 1}
	c.Signature = ed25519.Sign(d.key, c.SigningBytes())
	return c
}

func child(id, parent string) SyncNodeWrite {
	return SyncNodeWrite{NodeID: id, ParentID: &parent, Ciphertext: workspaceCipher()}
}

func workspaceDrivers(t *testing.T, store *Store, vault string) map[string]string {
	t.Helper()
	workspaces, err := store.BrowserSyncWorkspaces(context.Background(), "owner", vault)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, workspace := range workspaces {
		driver := ""
		if workspace.DriverDeviceID != nil {
			driver = *workspace.DriverDeviceID
		}
		out[workspace.WorkspaceID] = driver
	}
	return out
}

func setupWorkspaceVault(t *testing.T) (*Store, string, []*workspaceTestDevice) {
	t.Helper()
	store, _ := syncTestDatabase(t)
	ctx := context.Background()
	root, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	vault := uuid.NewString()
	devices := []*workspaceTestDevice{}
	for i := 0; i < 3; i++ {
		g, key := syncTestGrant(vault, rootKey)
		if i == 0 {
			if err := store.CreateBrowserSyncVault(ctx, "owner", root, syncTestKeyEnvelope(), g); err != nil {
				t.Fatal(err)
			}
		} else if err := store.EnrollBrowserSyncDevice(ctx, "owner", g); err != nil {
			t.Fatal(err)
		}
		devices = append(devices, &workspaceTestDevice{grant: g, key: key})
	}
	return store, vault, devices
}

func TestBrowserSyncWorkspaceLocksAndCAS(t *testing.T) {
	store, vault, d := setupWorkspaceVault(t)
	ctx := context.Background()
	a, b, c := d[0], d[1], d[2]
	drivers := workspaceDrivers(t, store, vault)
	if len(drivers) != 4 || drivers[vault] != "" {
		t.Fatalf("expected shared workspace plus one workspace per device: %v", drivers)
	}
	for _, dev := range d {
		if drivers[dev.grant.DeviceID] != dev.grant.DeviceID {
			t.Fatalf("new device must drive its own workspace: %v", drivers)
		}
	}
	workspaceA := a.grant.DeviceID
	window, tab := uuid.NewString(), uuid.NewString()
	first := a.op(workspaceA, 0, []SyncNodeWrite{child(window, workspaceA), child(tab, window)}, []SyncSlotWrite{{ViewNodeID: tab, Slot: SyncSlotPageState, Ciphertext: workspaceCipher()}}, nil)
	if _, err := store.PublishBrowserSyncWorkspace(ctx, "owner", first); !errors.Is(err, ErrSyncWorkspaceMode) {
		t.Fatalf("workspace op before workspace mode: %v", err)
	}
	if err := store.EnableBrowserSyncWorkspaceMode(ctx, "owner", vault); err != nil {
		t.Fatal(err)
	}
	// The log keeps carrying account-wide credentials in workspace mode, from any
	// full-sync device, on a counter independent of workspace ops.
	if r, err := store.PublishBrowserSync(ctx, "owner", syncTestMutation(b.grant, b.key, 1)); err != nil || r.Discarded {
		t.Fatalf("credential log write in workspace mode: %+v %v", r, err)
	}
	receipt, err := store.PublishBrowserSyncWorkspace(ctx, "owner", first)
	if err != nil || receipt.Discarded || receipt.WorkspaceVersion != 1 {
		t.Fatalf("first workspace op: %+v %v", receipt, err)
	}
	if retry, err := store.PublishBrowserSyncWorkspace(ctx, "owner", first); err != nil || retry.Sequence != receipt.Sequence {
		t.Fatalf("lost-ack retry changed receipt: %+v %v", retry, err)
	}
	// Workspace changes never advance the credential log's head: its replay must
	// stay gap-free.
	if ws, err := store.BrowserSyncVault(ctx, "owner"); err != nil || ws.HeadSequence != 1 {
		t.Fatalf("workspace op moved the credential log head: %+v %v", ws, err)
	}
	// Another device may edit A's workspace; a stale base loses on version only.
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", b.op(workspaceA, 0, nil, nil, nil)); err != nil || !r.Discarded || r.Reason != SyncDiscardWorkspaceVersion || r.WorkspaceVersion != 1 {
		t.Fatalf("non-driver stale write: %+v %v", r, err)
	}
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", a.op(workspaceA, 0, nil, nil, nil)); err != nil || !r.Discarded || r.Reason != SyncDiscardWorkspaceVersion {
		t.Fatalf("stale base version: %+v %v", r, err)
	}
	// Deleting the window alone would orphan the tab.
	orphan := a.op(workspaceA, 1, nil, nil, []string{window})
	if _, err := store.PublishBrowserSyncWorkspace(ctx, "owner", orphan); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("orphaning delete accepted: %v", err)
	}
	a.counter--
	x, y := uuid.NewString(), uuid.NewString()
	cycle := a.op(workspaceA, 1, []SyncNodeWrite{child(x, y), child(y, x)}, nil, nil)
	if _, err := store.PublishBrowserSyncWorkspace(ctx, "owner", cycle); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("parent cycle accepted: %v", err)
	}
	a.counter--

	// B takes over A's workspace: A loses its seat, and B's own workspace becomes free.
	if r, err := store.ClaimBrowserSyncWorkspace(ctx, "owner", b.claim(workspaceA, "")); err != nil || r.Discarded {
		t.Fatalf("claim: %+v %v", r, err)
	}
	drivers = workspaceDrivers(t, store, vault)
	if drivers[workspaceA] != b.grant.DeviceID || drivers[b.grant.DeviceID] != "" {
		t.Fatalf("claim did not move the driver seat: %v", drivers)
	}
	// Losing the seat no longer blocks edits, only a stale base does.
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", a.op(workspaceA, 0, nil, nil, nil)); err != nil || r.Reason != SyncDiscardWorkspaceVersion {
		t.Fatalf("displaced driver stale write: %+v %v", r, err)
	}
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", b.op(workspaceA, 1, nil, nil, []string{tab, window})); err != nil || r.Discarded || r.WorkspaceVersion != 2 {
		t.Fatalf("new driver write: %+v %v", r, err)
	}
	// C claims the same workspace: the last explicit claim wins, B is displaced.
	if _, err := store.ClaimBrowserSyncWorkspace(ctx, "owner", c.claim(workspaceA, "")); err != nil {
		t.Fatal(err)
	}
	drivers = workspaceDrivers(t, store, vault)
	if drivers[workspaceA] != c.grant.DeviceID || drivers[c.grant.DeviceID] != "" {
		t.Fatalf("second claim: %v", drivers)
	}
	seats := map[string]int{}
	for _, driver := range drivers {
		if driver != "" {
			seats[driver]++
		}
	}
	for device, n := range seats {
		if n > 1 {
			t.Fatalf("device %s drives %d workspaces", device, n)
		}
	}
	// The shared workspace accepts any full-sync device, still compare-and-swap.
	group := uuid.NewString()
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", a.op(vault, 0, []SyncNodeWrite{child(group, vault)}, nil, nil)); err != nil || r.Discarded {
		t.Fatalf("shared write: %+v %v", r, err)
	}
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", b.op(vault, 0, nil, nil, nil)); err != nil || r.Reason != SyncDiscardWorkspaceVersion {
		t.Fatalf("concurrent shared write not rejected: %+v %v", r, err)
	}
}

func TestBrowserSyncWorkspaceDeltaSnapshotAndSlots(t *testing.T) {
	store, vault, d := setupWorkspaceVault(t)
	ctx := context.Background()
	a := d[0]
	workspace := a.grant.DeviceID
	if err := store.EnableBrowserSyncWorkspaceMode(ctx, "owner", vault); err != nil {
		t.Fatal(err)
	}
	window, tab := uuid.NewString(), uuid.NewString()
	ops := []SyncWorkspaceOp{
		a.op(workspace, 0, []SyncNodeWrite{child(window, workspace), child(tab, window)}, []SyncSlotWrite{{ViewNodeID: tab, Slot: SyncSlotHistory, Ciphertext: workspaceCipher()}}, nil),
	}
	if _, err := store.PublishBrowserSyncWorkspace(ctx, "owner", ops[0]); err != nil {
		t.Fatal(err)
	}
	second := a.op(workspace, 1, []SyncNodeWrite{child(tab, window)}, []SyncSlotWrite{{ViewNodeID: tab, Slot: SyncSlotHistory}}, nil)
	if _, err := store.PublishBrowserSyncWorkspace(ctx, "owner", second); err != nil {
		t.Fatal(err)
	}
	delta, err := store.BrowserSyncWorkspaceDelta(ctx, "owner", vault, a.grant.DeviceID, workspace, 0)
	if err != nil {
		t.Fatal(err)
	}
	if delta.Version != 2 || len(delta.Changes) != 2 || len(delta.Nodes) != 3 || len(delta.Slots) != 0 {
		t.Fatalf("delta: version=%d changes=%d nodes=%d slots=%d", delta.Version, len(delta.Changes), len(delta.Nodes), len(delta.Slots))
	}
	if string(delta.Changes[1].Signature) != string(second.Signature) || delta.Changes[1].Manifest.Slots[0][2] != "" {
		t.Fatal("change feed lost the signed manifest")
	}
	if up, err := store.BrowserSyncWorkspaceDelta(ctx, "owner", vault, a.grant.DeviceID, workspace, 2); err != nil || len(up.Changes) != 0 {
		t.Fatalf("current client delta: %+v %v", up, err)
	}
	if _, err := store.BrowserSyncWorkspaceDelta(ctx, "owner", vault, a.grant.DeviceID, workspace, 3); !errors.Is(err, ErrSyncCursor) {
		t.Fatalf("future cursor: %v", err)
	}
	snapshot, err := store.BrowserSyncWorkspaceSnapshot(ctx, "owner", vault, d[1].grant.DeviceID, workspace)
	if err != nil || snapshot.Version != 2 || len(snapshot.Nodes) != 3 || snapshot.LastChange == nil || snapshot.LastChange.WorkspaceVersion != 2 {
		t.Fatalf("snapshot: %+v %v", snapshot, err)
	}
	third := a.op(workspace, 2, nil, []SyncSlotWrite{{ViewNodeID: tab, Slot: SyncSlotPageState, Ciphertext: workspaceCipher()}}, nil)
	if _, err := store.PublishBrowserSyncWorkspace(ctx, "owner", third); err != nil {
		t.Fatal(err)
	}
	slot, err := store.BrowserSyncSlot(ctx, "owner", vault, a.grant.DeviceID, workspace, tab, SyncSlotPageState)
	if err != nil || slot == nil || slot.Version != 3 || string(slot.Ciphertext) != string(third.Slots[0].Ciphertext) {
		t.Fatalf("slot: %+v %v", slot, err)
	}
	if missing, err := store.BrowserSyncSlot(ctx, "owner", vault, a.grant.DeviceID, workspace, tab, SyncSlotHistory); err != nil || missing != nil {
		t.Fatalf("deleted slot returned: %+v %v", missing, err)
	}
	if _, err := store.BrowserSyncWorkspaceSnapshot(ctx, "other", vault, a.grant.DeviceID, workspace); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("cross-account snapshot: %v", err)
	}
}

func TestBrowserSyncWorkspaceRemoteClaimRequests(t *testing.T) {
	store, vault, d := setupWorkspaceVault(t)
	ctx := context.Background()
	a, b := d[0], d[1]
	if err := store.EnableBrowserSyncWorkspaceMode(ctx, "owner", vault); err != nil {
		t.Fatal(err)
	}
	version := 1
	for _, dev := range d {
		if _, err := store.ControlBrowserSyncDevice(ctx, "owner", SyncDeviceControl{DeviceID: dev.grant.DeviceID, ControlVersion: &version}); err != nil {
			t.Fatal(err)
		}
	}
	// Remote requests require the target to be online.
	if err := store.BrowserSyncHeartbeat(ctx, SyncConnectionIdentity{UserID: "owner", VaultID: vault, DeviceID: b.grant.DeviceID}, uuid.NewString(), 0, true); err != nil {
		t.Fatal(err)
	}
	workspaceA := a.grant.DeviceID
	request, err := store.ControlBrowserSyncDevice(ctx, "owner", SyncDeviceControl{DeviceID: b.grant.DeviceID, Activate: true, WorkspaceID: &workspaceA})
	if err != nil || request == "" {
		t.Fatalf("activation request: %q %v", request, err)
	}
	// A claim for a different workspace under the request's ID is stale.
	if r, err := store.ClaimBrowserSyncWorkspace(ctx, "owner", b.claim(vault, request)); err == nil || r != nil {
		// The shared workspace is never claimable.
		t.Fatalf("shared workspace claim: %+v %v", r, err)
	}
	b.counter--
	if r, err := store.ClaimBrowserSyncWorkspace(ctx, "owner", b.claim(b.grant.DeviceID, request)); err != nil || r.Reason != SyncDiscardStaleClaim {
		t.Fatalf("mismatched workspace claim: %+v %v", r, err)
	}
	next, err := store.ControlBrowserSyncDevice(ctx, "owner", SyncDeviceControl{DeviceID: b.grant.DeviceID, Activate: true, WorkspaceID: &workspaceA})
	if err != nil {
		t.Fatal(err)
	}
	if r, err := store.ClaimBrowserSyncWorkspace(ctx, "owner", b.claim(workspaceA, next)); err != nil || r.Discarded {
		t.Fatalf("requested claim: %+v %v", r, err)
	}
	if workspaceDrivers(t, store, vault)[workspaceA] != b.grant.DeviceID {
		t.Fatal("requested claim did not take effect")
	}
}

func TestBrowserSyncBlobsStayInVault(t *testing.T) {
	store, vault, d := setupWorkspaceVault(t)
	ctx := context.Background()
	hash := make([]byte, 32)
	hash[0] = 7
	blob := SyncBlob{Hash: hash, Ciphertext: workspaceCipher()}
	if err := store.PutBrowserSyncBlob(ctx, "other", vault, d[0].grant.DeviceID, blob); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("cross-account blob write: %v", err)
	}
	if err := store.PutBrowserSyncBlob(ctx, "owner", vault, d[0].grant.DeviceID, blob); err != nil {
		t.Fatal(err)
	}
	// First writer wins: a second upload under the same hash keeps content.
	if err := store.PutBrowserSyncBlob(ctx, "owner", vault, d[1].grant.DeviceID, SyncBlob{Hash: hash, Ciphertext: workspaceCipher()}); err != nil {
		t.Fatal(err)
	}
	blobs, err := store.BrowserSyncBlobs(ctx, "owner", vault, d[2].grant.DeviceID, [][]byte{hash})
	if err != nil || len(blobs) != 1 || string(blobs[0].Ciphertext) != string(blob.Ciphertext) {
		t.Fatalf("blob read: %+v %v", blobs, err)
	}
}

// Two machines edit one workspace at the same time. The server orders them:
// the first write wins, the second learns the head it lost to, rebases and
// lands next. Neither needs the workspace's seat.
func TestBrowserSyncWorkspaceConcurrentWriters(t *testing.T) {
	store, vault, d := setupWorkspaceVault(t)
	ctx := context.Background()
	a, b := d[0], d[1]
	workspace := a.grant.DeviceID
	if err := store.EnableBrowserSyncWorkspaceMode(ctx, "owner", vault); err != nil {
		t.Fatal(err)
	}
	window, tab := uuid.NewString(), uuid.NewString()
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", a.op(workspace, 0, []SyncNodeWrite{child(window, workspace)}, nil, nil)); err != nil || r.Discarded {
		t.Fatalf("first writer: %+v %v", r, err)
	}
	// B built its edit on version 0 too.
	lost, err := store.PublishBrowserSyncWorkspace(ctx, "owner", b.op(workspace, 0, []SyncNodeWrite{child(tab, workspace)}, nil, nil))
	if err != nil || !lost.Discarded || lost.Reason != SyncDiscardWorkspaceVersion || lost.WorkspaceVersion != 1 {
		t.Fatalf("second writer on a stale base: %+v %v", lost, err)
	}
	// Rebased onto the head it was told about, B's edit lands.
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", b.op(workspace, lost.WorkspaceVersion, []SyncNodeWrite{child(tab, window)}, nil, nil)); err != nil || r.Discarded || r.WorkspaceVersion != 2 {
		t.Fatalf("rebased second writer: %+v %v", r, err)
	}
	// B does not hold A's sign-in lease, so it cannot write A's sign-ins.
	signin := b.op(workspace, 2, nil, []SyncSlotWrite{{ViewNodeID: workspace, Slot: SyncSlotSigninFirst, Ciphertext: workspaceCipher()}}, nil)
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", signin); err != nil || r.Reason != SyncDiscardNotDriver {
		t.Fatalf("sign-in write without the lease: %+v %v", r, err)
	}
}
