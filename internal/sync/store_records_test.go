package browsersync

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func recordKey(c string) string { return strings.Repeat(c, 64) }

func TestBrowserSyncRecordsCompareAndSwapAndPull(t *testing.T) {
	store, vault, d := setupWorkspaceVault(t)
	ctx := context.Background()
	a, b := d[0].grant.DeviceID, d[1].grant.DeviceID
	key := recordKey("a")

	results, cursor, err := store.PushBrowserSyncRecords(ctx, "owner", vault, a, "bookmarks", []SyncRecordWrite{{Key: key, Ciphertext: []byte("v1")}})
	if err != nil || !results[0].Applied || results[0].Version != 1 || cursor != 1 {
		t.Fatalf("create: %+v %d %v", results, cursor, err)
	}
	// B edited from the same base: it loses and learns the current row.
	results, _, err = store.PushBrowserSyncRecords(ctx, "owner", vault, b, "bookmarks", []SyncRecordWrite{{Key: key, Ciphertext: []byte("b")}})
	if err != nil || results[0].Applied || results[0].Current == nil || !bytes.Equal(results[0].Current.Ciphertext, []byte("v1")) {
		t.Fatalf("stale write: %+v %v", results, err)
	}
	// Rebased onto version 1, it lands.
	results, _, err = store.PushBrowserSyncRecords(ctx, "owner", vault, b, "bookmarks", []SyncRecordWrite{{Key: key, BaseVersion: 1, Ciphertext: []byte("v2")}})
	if err != nil || !results[0].Applied || results[0].Version != 2 {
		t.Fatalf("rebased write: %+v %v", results, err)
	}
	// A deletion is a row other devices read, not a missing one.
	other := recordKey("b")
	if _, _, err = store.PushBrowserSyncRecords(ctx, "owner", vault, a, "bookmarks", []SyncRecordWrite{{Key: other, Ciphertext: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.PushBrowserSyncRecords(ctx, "owner", vault, a, "bookmarks", []SyncRecordWrite{{Key: other, BaseVersion: 1}}); err != nil {
		t.Fatal(err)
	}
	page, err := store.PullBrowserSyncRecords(ctx, "owner", vault, b, "bookmarks", 0, 0)
	if err != nil || len(page.Records) != 2 || page.More || page.Reset {
		t.Fatalf("pull: %+v %v", page, err)
	}
	byKey := map[string]SyncRecord{}
	for _, r := range page.Records {
		byKey[r.Key] = r
	}
	if byKey[key].Version != 2 || byKey[other].Ciphertext != nil {
		t.Fatalf("pulled rows: %+v", byKey)
	}
	// Nothing after the cursor; paging stops.
	if next, err := store.PullBrowserSyncRecords(ctx, "owner", vault, b, "bookmarks", page.Cursor, 0); err != nil || len(next.Records) != 0 || next.Cursor != page.Cursor {
		t.Fatalf("pull at head: %+v %v", next, err)
	}
	// Collections are separate feeds.
	if tabs, err := store.PullBrowserSyncRecords(ctx, "owner", vault, b, "tab_groups", 0, 0); err != nil || len(tabs.Records) != 0 {
		t.Fatalf("other collection: %+v %v", tabs, err)
	}
	// Paging: one record per page walks the feed in order.
	first, err := store.PullBrowserSyncRecords(ctx, "owner", vault, b, "bookmarks", 0, 1)
	if err != nil || len(first.Records) != 1 || !first.More {
		t.Fatalf("first page: %+v %v", first, err)
	}
}

func TestBrowserSyncRecordsRejectInvalidInput(t *testing.T) {
	store, vault, d := setupWorkspaceVault(t)
	ctx := context.Background()
	a := d[0].grant.DeviceID
	for name, writes := range map[string][]SyncRecordWrite{
		"readable key": {{Key: "bookmark-1", Ciphertext: []byte("x")}},
		"oversized":    {{Key: recordKey("c"), Ciphertext: make([]byte, syncRecordMaxBytes+1)}},
	} {
		if _, _, err := store.PushBrowserSyncRecords(ctx, "owner", vault, a, "bookmarks", writes); !errors.Is(err, ErrSyncInvalid) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	if _, _, err := store.PushBrowserSyncRecords(ctx, "owner", vault, a, "unknown", []SyncRecordWrite{{Key: recordKey("d"), Ciphertext: []byte("x")}}); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("unknown collection accepted: %v", err)
	}
	if _, err := store.PullBrowserSyncRecords(ctx, "owner", vault, a, "bookmarks", 5, 0); !errors.Is(err, ErrSyncCursor) {
		t.Fatalf("future cursor: %v", err)
	}
	if _, _, err := store.PushBrowserSyncRecords(ctx, "someone-else", vault, a, "bookmarks", []SyncRecordWrite{{Key: recordKey("e"), Ciphertext: []byte("x")}}); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("another account wrote: %v", err)
	}
}

// Once every active device keeps bookmarks in the cold store, the shared workspace
// (where they used to live) takes no more writes.
func TestBrowserSyncSharedWorkspaceRetiresWhenEveryDeviceMoved(t *testing.T) {
	store, vault, d := setupWorkspaceVault(t)
	ctx := context.Background()
	if err := store.EnableBrowserSyncWorkspaceMode(ctx, "owner", vault); err != nil {
		t.Fatal(err)
	}
	for _, device := range d[:2] {
		if err := store.MarkBrowserSyncRecordsCapable(ctx, "owner", vault, device.grant.DeviceID); err != nil {
			t.Fatal(err)
		}
	}
	// One device still on an older version: the shared workspace stays writable.
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", d[0].op(vault, 0, nil, nil, nil)); err != nil || r.Discarded {
		t.Fatalf("shared write before every device moved: %+v %v", r, err)
	}
	if err := store.MarkBrowserSyncRecordsCapable(ctx, "owner", vault, d[2].grant.DeviceID); err != nil {
		t.Fatal(err)
	}
	devices, err := store.BrowserSyncDevices(ctx, "owner", vault)
	if err != nil {
		t.Fatal(err)
	}
	for _, device := range devices {
		if !device.UsesCollections {
			t.Fatalf("device %s not marked", device.DeviceID)
		}
	}
	if r, err := store.PublishBrowserSyncWorkspace(ctx, "owner", d[0].op(vault, 1, nil, nil, nil)); err != nil || r.Reason != SyncDiscardSharedRetired {
		t.Fatalf("shared write after retirement: %+v %v", r, err)
	}
}
