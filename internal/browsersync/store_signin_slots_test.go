package browsersync

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"
)

// signinCipher is a sealed shard at the largest frame native clients send:
// a 512 KiB padded frame plus nonce and tag.
func signinCipher() []byte {
	out := make([]byte, 512<<10+28)
	_, _ = rand.Read(out)
	return out
}

// A device's sign-in data sits in slots on its workspace's root node. Only the
// session holding that workspace's lock may write it; any device may read it.
func TestBrowserSyncDeviceSigninSlotsFollowTheWorkspaceLock(t *testing.T) {
	store, vault, d := setupWorkspaceVault(t)
	ctx := context.Background()
	a, b := d[0], d[1]
	workspaceA := a.grant.DeviceID
	if err := store.EnableBrowserSyncWorkspaceMode(ctx, "owner", vault); err != nil {
		t.Fatal(err)
	}
	first, second := signinCipher(), signinCipher()
	op := a.op(workspaceA, 0, nil, []SyncSlotWrite{
		{ViewNodeID: workspaceA, Slot: SyncSlotSigninFirst, Ciphertext: first},
		{ViewNodeID: workspaceA, Slot: SyncSlotSigninLast, Ciphertext: second},
	}, nil)
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", op); err != nil || r.Discarded {
		t.Fatalf("lock holder's sign-in write: %+v %v", r, err)
	}
	slot, err := store.BrowserSyncSlot(ctx, "owner", vault, b.grant.DeviceID, workspaceA, workspaceA, SyncSlotSigninFirst)
	if err != nil || slot == nil || !bytes.Equal(slot.Ciphertext, first) {
		t.Fatalf("another device reads the sign-in shard: %v %v", slot, err)
	}
	// Without the lock, sign-in data cannot be written.
	intruder := b.op(workspaceA, 1, nil, []SyncSlotWrite{{ViewNodeID: workspaceA, Slot: SyncSlotSigninFirst, Ciphertext: workspaceCipher()}}, nil)
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", intruder); err != nil || r.Reason != SyncDiscardNotDriver {
		t.Fatalf("non-holder sign-in write: %+v %v", r, err)
	}
	// Taking the lock transfers the right to write, and only that.
	if r, err := store.ClaimBrowserSyncWorkspace(ctx, "owner", b.claim(workspaceA, "")); err != nil || r.Discarded {
		t.Fatalf("claim: %+v %v", r, err)
	}
	replaced := signinCipher()
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", b.op(workspaceA, 1, nil, []SyncSlotWrite{{ViewNodeID: workspaceA, Slot: SyncSlotSigninFirst, Ciphertext: replaced}}, nil)); err != nil || r.Discarded {
		t.Fatalf("new holder's sign-in write: %+v %v", r, err)
	}
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", a.op(workspaceA, 2, nil, []SyncSlotWrite{{ViewNodeID: workspaceA, Slot: SyncSlotSigninFirst, Ciphertext: workspaceCipher()}}, nil)); err != nil || r.Reason != SyncDiscardNotDriver {
		t.Fatalf("displaced holder still writes sign-in data: %+v %v", r, err)
	}
	slot, err = store.BrowserSyncSlot(ctx, "owner", vault, a.grant.DeviceID, workspaceA, workspaceA, SyncSlotSigninFirst)
	if err != nil || slot == nil || !bytes.Equal(slot.Ciphertext, replaced) {
		t.Fatalf("sign-in shard after takeover: %v %v", slot, err)
	}
	// Three full shards exceed one op: clients must spread them across ops.
	tooBig := b.op(workspaceA, 2, nil, []SyncSlotWrite{
		{ViewNodeID: workspaceA, Slot: 5, Ciphertext: signinCipher()},
		{ViewNodeID: workspaceA, Slot: 6, Ciphertext: signinCipher()},
		{ViewNodeID: workspaceA, Slot: 7, Ciphertext: signinCipher()},
	}, nil)
	if _, err := store.PublishBrowserSyncWorkspace(ctx, "owner", tooBig); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("oversized op accepted: %v", err)
	}
	b.counter--
	// Slot kinds stop at the protocol maximum.
	beyond := b.op(workspaceA, 2, nil, []SyncSlotWrite{{ViewNodeID: workspaceA, Slot: SyncSlotSigninLast + 1, Ciphertext: workspaceCipher()}}, nil)
	if _, err := store.PublishBrowserSyncWorkspace(ctx, "owner", beyond); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("slot kind past the maximum accepted: %v", err)
	}
}
