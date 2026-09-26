package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type treeTestDevice struct {
	grant   SyncDeviceGrant
	key     ed25519.PrivateKey
	counter int64
}

func treeCipher() []byte {
	out := make([]byte, 40)
	_, _ = rand.Read(out)
	return out
}

func (d *treeTestDevice) op(tree string, base int64, upserts []SyncNodeWrite, slots []SyncSlotWrite, deletes []string) SyncTreeOp {
	d.counter++
	op := SyncTreeOp{WorkspaceID: d.grant.WorkspaceID, TreeID: tree, OperationID: uuid.NewString(), DeviceID: d.grant.DeviceID, DeviceCounter: d.counter, KeyEpoch: 1, BaseTreeVersion: base, MerkleRoot: make([]byte, 32), Upserts: append([]SyncNodeWrite{{NodeID: tree, Ciphertext: treeCipher()}}, upserts...), Slots: slots, Deletes: deletes}
	op.Signature = ed25519.Sign(d.key, op.SigningBytes())
	return op
}

func (d *treeTestDevice) claim(tree, operation string) SyncTreeClaim {
	d.counter++
	if operation == "" {
		operation = uuid.NewString()
	}
	c := SyncTreeClaim{WorkspaceID: d.grant.WorkspaceID, TreeID: tree, OperationID: operation, DeviceID: d.grant.DeviceID, DeviceCounter: d.counter, KeyEpoch: 1}
	c.Signature = ed25519.Sign(d.key, c.SigningBytes())
	return c
}

func child(id, parent string) SyncNodeWrite {
	return SyncNodeWrite{NodeID: id, ParentID: &parent, Ciphertext: treeCipher()}
}

func treeDrivers(t *testing.T, store *Store, workspace string) map[string]string {
	t.Helper()
	trees, err := store.BrowserSyncTrees(context.Background(), "owner", workspace)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, tree := range trees {
		driver := ""
		if tree.DriverDeviceID != nil {
			driver = *tree.DriverDeviceID
		}
		out[tree.TreeID] = driver
	}
	return out
}

func setupTreeWorkspace(t *testing.T) (*Store, string, []*treeTestDevice) {
	t.Helper()
	store, _ := syncTestDatabase(t)
	ctx := context.Background()
	root, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	workspace := uuid.NewString()
	devices := []*treeTestDevice{}
	for i := 0; i < 3; i++ {
		g, key := syncTestGrant(workspace, rootKey)
		if i == 0 {
			if err := store.CreateBrowserSyncWorkspace(ctx, "owner", root, syncTestKeyEnvelope(), g); err != nil {
				t.Fatal(err)
			}
		} else if err := store.EnrollBrowserSyncDevice(ctx, "owner", g); err != nil {
			t.Fatal(err)
		}
		devices = append(devices, &treeTestDevice{grant: g, key: key})
	}
	return store, workspace, devices
}

func TestBrowserSyncTreeLocksAndCAS(t *testing.T) {
	store, workspace, d := setupTreeWorkspace(t)
	ctx := context.Background()
	a, b, c := d[0], d[1], d[2]
	drivers := treeDrivers(t, store, workspace)
	if len(drivers) != 4 || drivers[workspace] != "" {
		t.Fatalf("expected shared tree plus one tree per device: %v", drivers)
	}
	for _, dev := range d {
		if drivers[dev.grant.DeviceID] != dev.grant.DeviceID {
			t.Fatalf("new device must drive its own tree: %v", drivers)
		}
	}
	treeA := a.grant.DeviceID
	window, tab := uuid.NewString(), uuid.NewString()
	first := a.op(treeA, 0, []SyncNodeWrite{child(window, treeA), child(tab, window)}, []SyncSlotWrite{{TabNodeID: tab, Slot: SyncSlotPageState, Ciphertext: treeCipher()}}, nil)
	if _, err := store.PublishBrowserSyncTree(ctx, "owner", first); !errors.Is(err, ErrSyncTreeMode) {
		t.Fatalf("tree op before tree mode: %v", err)
	}
	if err := store.EnableBrowserSyncTreeMode(ctx, "owner", workspace); err != nil {
		t.Fatal(err)
	}
	// The log keeps carrying account-wide credentials in tree mode, from any
	// full-sync device, on a counter independent of tree ops.
	if r, err := store.PublishBrowserSync(ctx, "owner", syncTestMutation(b.grant, b.key, 1)); err != nil || r.Discarded {
		t.Fatalf("credential log write in tree mode: %+v %v", r, err)
	}
	receipt, err := store.PublishBrowserSyncTree(ctx, "owner", first)
	if err != nil || receipt.Discarded || receipt.TreeVersion != 1 {
		t.Fatalf("first tree op: %+v %v", receipt, err)
	}
	if retry, err := store.PublishBrowserSyncTree(ctx, "owner", first); err != nil || retry.Sequence != receipt.Sequence {
		t.Fatalf("lost-ack retry changed receipt: %+v %v", retry, err)
	}
	// Tree changes never advance the credential log's head: its replay must
	// stay gap-free.
	if ws, err := store.BrowserSyncWorkspace(ctx, "owner"); err != nil || ws.HeadSequence != 1 {
		t.Fatalf("tree op moved the credential log head: %+v %v", ws, err)
	}
	if r, err := store.PublishBrowserSyncTree(ctx, "owner", b.op(treeA, 1, nil, nil, nil)); err != nil || !r.Discarded || r.Reason != SyncDiscardNotDriver {
		t.Fatalf("non-driver write: %+v %v", r, err)
	}
	if r, err := store.PublishBrowserSyncTree(ctx, "owner", a.op(treeA, 0, nil, nil, nil)); err != nil || !r.Discarded || r.Reason != SyncDiscardTreeVersion {
		t.Fatalf("stale base version: %+v %v", r, err)
	}
	// Deleting the window alone would orphan the tab.
	orphan := a.op(treeA, 1, nil, nil, []string{window})
	if _, err := store.PublishBrowserSyncTree(ctx, "owner", orphan); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("orphaning delete accepted: %v", err)
	}
	a.counter--
	x, y := uuid.NewString(), uuid.NewString()
	cycle := a.op(treeA, 1, []SyncNodeWrite{child(x, y), child(y, x)}, nil, nil)
	if _, err := store.PublishBrowserSyncTree(ctx, "owner", cycle); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("parent cycle accepted: %v", err)
	}
	a.counter--

	// B takes over A's tree: A loses its seat, and B's own tree becomes free.
	if r, err := store.ClaimBrowserSyncTree(ctx, "owner", b.claim(treeA, "")); err != nil || r.Discarded {
		t.Fatalf("claim: %+v %v", r, err)
	}
	drivers = treeDrivers(t, store, workspace)
	if drivers[treeA] != b.grant.DeviceID || drivers[b.grant.DeviceID] != "" {
		t.Fatalf("claim did not move the driver seat: %v", drivers)
	}
	if r, err := store.PublishBrowserSyncTree(ctx, "owner", a.op(treeA, 1, nil, nil, nil)); err != nil || r.Reason != SyncDiscardNotDriver {
		t.Fatalf("displaced driver still writes: %+v %v", r, err)
	}
	if r, err := store.PublishBrowserSyncTree(ctx, "owner", b.op(treeA, 1, nil, nil, []string{tab, window})); err != nil || r.Discarded || r.TreeVersion != 2 {
		t.Fatalf("new driver write: %+v %v", r, err)
	}
	// C claims the same tree: the last explicit claim wins, B is displaced.
	if _, err := store.ClaimBrowserSyncTree(ctx, "owner", c.claim(treeA, "")); err != nil {
		t.Fatal(err)
	}
	drivers = treeDrivers(t, store, workspace)
	if drivers[treeA] != c.grant.DeviceID || drivers[c.grant.DeviceID] != "" {
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
			t.Fatalf("device %s drives %d trees", device, n)
		}
	}
	// The shared tree accepts any full-sync device, still compare-and-swap.
	group := uuid.NewString()
	if r, err := store.PublishBrowserSyncTree(ctx, "owner", a.op(workspace, 0, []SyncNodeWrite{child(group, workspace)}, nil, nil)); err != nil || r.Discarded {
		t.Fatalf("shared write: %+v %v", r, err)
	}
	if r, err := store.PublishBrowserSyncTree(ctx, "owner", b.op(workspace, 0, nil, nil, nil)); err != nil || r.Reason != SyncDiscardTreeVersion {
		t.Fatalf("concurrent shared write not rejected: %+v %v", r, err)
	}
}

func TestBrowserSyncTreeDeltaSnapshotAndSlots(t *testing.T) {
	store, workspace, d := setupTreeWorkspace(t)
	ctx := context.Background()
	a := d[0]
	tree := a.grant.DeviceID
	if err := store.EnableBrowserSyncTreeMode(ctx, "owner", workspace); err != nil {
		t.Fatal(err)
	}
	window, tab := uuid.NewString(), uuid.NewString()
	ops := []SyncTreeOp{
		a.op(tree, 0, []SyncNodeWrite{child(window, tree), child(tab, window)}, []SyncSlotWrite{{TabNodeID: tab, Slot: SyncSlotHistory, Ciphertext: treeCipher()}}, nil),
	}
	if _, err := store.PublishBrowserSyncTree(ctx, "owner", ops[0]); err != nil {
		t.Fatal(err)
	}
	second := a.op(tree, 1, []SyncNodeWrite{child(tab, window)}, []SyncSlotWrite{{TabNodeID: tab, Slot: SyncSlotHistory}}, nil)
	if _, err := store.PublishBrowserSyncTree(ctx, "owner", second); err != nil {
		t.Fatal(err)
	}
	delta, err := store.BrowserSyncTreeDelta(ctx, "owner", workspace, a.grant.DeviceID, tree, 0)
	if err != nil {
		t.Fatal(err)
	}
	if delta.Version != 2 || len(delta.Changes) != 2 || len(delta.Nodes) != 3 || len(delta.Slots) != 0 {
		t.Fatalf("delta: version=%d changes=%d nodes=%d slots=%d", delta.Version, len(delta.Changes), len(delta.Nodes), len(delta.Slots))
	}
	if string(delta.Changes[1].Signature) != string(second.Signature) || delta.Changes[1].Manifest.Slots[0][2] != "" {
		t.Fatal("change feed lost the signed manifest")
	}
	if up, err := store.BrowserSyncTreeDelta(ctx, "owner", workspace, a.grant.DeviceID, tree, 2); err != nil || len(up.Changes) != 0 {
		t.Fatalf("current client delta: %+v %v", up, err)
	}
	if _, err := store.BrowserSyncTreeDelta(ctx, "owner", workspace, a.grant.DeviceID, tree, 3); !errors.Is(err, ErrSyncCursor) {
		t.Fatalf("future cursor: %v", err)
	}
	snapshot, err := store.BrowserSyncTreeSnapshot(ctx, "owner", workspace, d[1].grant.DeviceID, tree)
	if err != nil || snapshot.Version != 2 || len(snapshot.Nodes) != 3 || snapshot.LastChange == nil || snapshot.LastChange.TreeVersion != 2 {
		t.Fatalf("snapshot: %+v %v", snapshot, err)
	}
	third := a.op(tree, 2, nil, []SyncSlotWrite{{TabNodeID: tab, Slot: SyncSlotPageState, Ciphertext: treeCipher()}}, nil)
	if _, err := store.PublishBrowserSyncTree(ctx, "owner", third); err != nil {
		t.Fatal(err)
	}
	slot, err := store.BrowserSyncSlot(ctx, "owner", workspace, a.grant.DeviceID, tree, tab, SyncSlotPageState)
	if err != nil || slot == nil || slot.Version != 3 || string(slot.Ciphertext) != string(third.Slots[0].Ciphertext) {
		t.Fatalf("slot: %+v %v", slot, err)
	}
	if missing, err := store.BrowserSyncSlot(ctx, "owner", workspace, a.grant.DeviceID, tree, tab, SyncSlotHistory); err != nil || missing != nil {
		t.Fatalf("deleted slot returned: %+v %v", missing, err)
	}
	if _, err := store.BrowserSyncTreeSnapshot(ctx, "other", workspace, a.grant.DeviceID, tree); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("cross-account snapshot: %v", err)
	}
}

func TestBrowserSyncTreeRemoteClaimRequests(t *testing.T) {
	store, workspace, d := setupTreeWorkspace(t)
	ctx := context.Background()
	a, b := d[0], d[1]
	if err := store.EnableBrowserSyncTreeMode(ctx, "owner", workspace); err != nil {
		t.Fatal(err)
	}
	version := 1
	for _, dev := range d {
		if _, err := store.ControlBrowserSyncDevice(ctx, "owner", SyncDeviceControl{DeviceID: dev.grant.DeviceID, ControlVersion: &version}); err != nil {
			t.Fatal(err)
		}
	}
	// Remote requests require the target to be online.
	if err := store.BrowserSyncHeartbeat(ctx, SyncConnectionIdentity{UserID: "owner", WorkspaceID: workspace, DeviceID: b.grant.DeviceID}, uuid.NewString(), 0, true); err != nil {
		t.Fatal(err)
	}
	treeA := a.grant.DeviceID
	request, err := store.ControlBrowserSyncDevice(ctx, "owner", SyncDeviceControl{DeviceID: b.grant.DeviceID, Activate: true, TreeID: &treeA})
	if err != nil || request == "" {
		t.Fatalf("activation request: %q %v", request, err)
	}
	// A claim for a different tree under the request's ID is stale.
	if r, err := store.ClaimBrowserSyncTree(ctx, "owner", b.claim(workspace, request)); err == nil || r != nil {
		// The shared tree is never claimable.
		t.Fatalf("shared tree claim: %+v %v", r, err)
	}
	b.counter--
	if r, err := store.ClaimBrowserSyncTree(ctx, "owner", b.claim(b.grant.DeviceID, request)); err != nil || r.Reason != SyncDiscardStaleClaim {
		t.Fatalf("mismatched tree claim: %+v %v", r, err)
	}
	next, err := store.ControlBrowserSyncDevice(ctx, "owner", SyncDeviceControl{DeviceID: b.grant.DeviceID, Activate: true, TreeID: &treeA})
	if err != nil {
		t.Fatal(err)
	}
	if r, err := store.ClaimBrowserSyncTree(ctx, "owner", b.claim(treeA, next)); err != nil || r.Discarded {
		t.Fatalf("requested claim: %+v %v", r, err)
	}
	if treeDrivers(t, store, workspace)[treeA] != b.grant.DeviceID {
		t.Fatal("requested claim did not take effect")
	}
}

func TestBrowserSyncBlobsStayInWorkspace(t *testing.T) {
	store, workspace, d := setupTreeWorkspace(t)
	ctx := context.Background()
	hash := make([]byte, 32)
	hash[0] = 7
	blob := SyncBlob{Hash: hash, Ciphertext: treeCipher()}
	if err := store.PutBrowserSyncBlob(ctx, "other", workspace, d[0].grant.DeviceID, blob); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("cross-account blob write: %v", err)
	}
	if err := store.PutBrowserSyncBlob(ctx, "owner", workspace, d[0].grant.DeviceID, blob); err != nil {
		t.Fatal(err)
	}
	// First writer wins: a second upload under the same hash keeps content.
	if err := store.PutBrowserSyncBlob(ctx, "owner", workspace, d[1].grant.DeviceID, SyncBlob{Hash: hash, Ciphertext: treeCipher()}); err != nil {
		t.Fatal(err)
	}
	blobs, err := store.BrowserSyncBlobs(ctx, "owner", workspace, d[2].grant.DeviceID, [][]byte{hash})
	if err != nil || len(blobs) != 1 || string(blobs[0].Ciphertext) != string(blob.Ciphertext) {
		t.Fatalf("blob read: %+v %v", blobs, err)
	}
}
