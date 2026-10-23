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
	for _, collection := range []string{"Extensions", "site-permissions", "search_engines", "download"} {
		if _, _, err := store.PushBrowserSyncRecords(ctx, "owner", vault, a, collection, []SyncRecordWrite{{Key: recordKey("d"), Ciphertext: []byte("x")}}); !errors.Is(err, ErrSyncInvalid) {
			t.Fatalf("%s accepted: %v", collection, err)
		}
	}
	if _, err := store.PullBrowserSyncRecords(ctx, "owner", vault, a, "bookmarks", 5, 0); !errors.Is(err, ErrSyncCursor) {
		t.Fatalf("future cursor: %v", err)
	}
	if _, _, err := store.PushBrowserSyncRecords(ctx, "someone-else", vault, a, "bookmarks", []SyncRecordWrite{{Key: recordKey("e"), Ciphertext: []byte("x")}}); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("another account wrote: %v", err)
	}
}

// A Space's extensions, site permissions, search settings and downloads list
// each travel in their own sealed collection.
func TestBrowserSyncRecordsAcceptSpaceCollections(t *testing.T) {
	store, vault, d := setupWorkspaceVault(t)
	ctx := context.Background()
	a, b := d[0].grant.DeviceID, d[1].grant.DeviceID
	for _, collection := range []string{"extensions", "site_permissions", "search", "downloads"} {
		results, _, err := store.PushBrowserSyncRecords(ctx, "owner", vault, a, collection, []SyncRecordWrite{{Key: recordKey("f"), Ciphertext: []byte(collection)}})
		if err != nil || !results[0].Applied || results[0].Version != 1 {
			t.Fatalf("%s push: %+v %v", collection, results, err)
		}
		page, err := store.PullBrowserSyncRecords(ctx, "owner", vault, b, collection, 0, 10)
		if err != nil || len(page.Records) != 1 || !bytes.Equal(page.Records[0].Ciphertext, []byte(collection)) {
			t.Fatalf("%s pull: %+v %v", collection, page, err)
		}
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

// clipboardPolicies adds the device tables the clipboard check reads, in the
// disposable schema, and turns the clipboard on for `allowed` devices.
func clipboardPolicies(t *testing.T, store *Store, allowed map[string]bool) {
	t.Helper()
	if _, err := store.Conn.Exec(`CREATE TABLE trusted_devices(id text primary key, user_id text not null, sync_device_id text, admission_state text not null);
		CREATE TABLE device_policies(device_id text primary key, user_id text not null, clipboard boolean not null)`); err != nil {
		t.Fatal(err)
	}
	for device, clipboard := range allowed {
		id := "device_" + device
		if _, err := store.Conn.Exec(`INSERT INTO trusted_devices VALUES($1,'owner',$2,'admitted'); INSERT INTO device_policies VALUES($1,'owner',$3)`, id, device, clipboard); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBrowserSyncClipboardRing(t *testing.T) {
	store, vault, d := setupWorkspaceVault(t)
	ctx := context.Background()
	a, b, c := d[0].grant.DeviceID, d[1].grant.DeviceID, d[2].grant.DeviceID
	clip := func(fill byte, size int) []SyncRecordWrite {
		return []SyncRecordWrite{{Key: recordKey(string(rune('a' + fill%6))), Ciphertext: bytes.Repeat([]byte{fill}, size)}}
	}
	// No device tables yet means no admitted device has the clipboard on.
	clipboardPolicies(t, store, map[string]bool{a: true, b: false})

	if _, _, err := store.PushBrowserSyncRecords(ctx, "owner", vault, b, "clipboard", clip(1, 10)); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("clipboard off in policy: %v", err)
	}
	if _, err := store.PullBrowserSyncRecords(ctx, "owner", vault, c, "clipboard", 0, 0); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("device without a policy read clips: %v", err)
	}
	if _, _, err := store.PushBrowserSyncRecords(ctx, "owner", vault, a, "clipboard", clip(1, syncClipboardMaxBytes+1)); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("oversized clip accepted: %v", err)
	}
	// Clips may be larger than other records, up to the clipboard's limit.
	results, _, err := store.PushBrowserSyncRecords(ctx, "owner", vault, a, "clipboard", clip(0, syncClipboardMaxBytes))
	if err != nil || !results[0].Applied {
		t.Fatalf("full-size clip: %+v %v", results, err)
	}
	if _, _, err := store.PushBrowserSyncRecords(ctx, "owner", vault, a, "bookmarks", clip(2, syncRecordMaxBytes+1)); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("other collections keep their limit: %v", err)
	}
	page, err := store.PullBrowserSyncRecords(ctx, "owner", vault, a, "clipboard", 0, 0)
	if err != nil || len(page.Records) != 1 || len(page.Records[0].Ciphertext) != syncClipboardMaxBytes {
		t.Fatalf("pull clip: %v %v", page, err)
	}

	// A day-old clip is never served, and the next write tombstones it.
	if _, err := store.Conn.Exec(`UPDATE browser_sync_records SET updated_at=now()-interval '25 hours' WHERE collection='clipboard'`); err != nil {
		t.Fatal(err)
	}
	if page, err := store.PullBrowserSyncRecords(ctx, "owner", vault, a, "clipboard", 0, 0); err != nil || len(page.Records) != 0 {
		t.Fatalf("expired clip served: %+v %v", page, err)
	}
	if _, _, err := store.PushBrowserSyncRecords(ctx, "owner", vault, a, "clipboard", clip(1, 10)); err != nil {
		t.Fatal(err)
	}
	page, err = store.PullBrowserSyncRecords(ctx, "owner", vault, a, "clipboard", 0, 0)
	if err != nil || len(page.Records) != 2 {
		t.Fatalf("after expiry: %+v %v", page, err)
	}
	for _, record := range page.Records {
		if record.Key == recordKey("a") && record.Ciphertext != nil {
			t.Fatalf("expired clip kept its ciphertext: %+v", record)
		}
	}

	// Large clips come a few at a time.
	for fill := byte(2); fill < 6; fill++ {
		if _, _, err := store.PushBrowserSyncRecords(ctx, "owner", vault, a, "clipboard", clip(fill, 10)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 6; i++ {
		write := []SyncRecordWrite{{Key: recordKey(string(rune('0' + i))), Ciphertext: []byte("x")}}
		if _, _, err := store.PushBrowserSyncRecords(ctx, "owner", vault, a, "clipboard", write); err != nil {
			t.Fatal(err)
		}
	}
	page, err = store.PullBrowserSyncRecords(ctx, "owner", vault, a, "clipboard", 0, 0)
	if err != nil || len(page.Records) != syncClipboardMaxPage || !page.More {
		t.Fatalf("clipboard paging: %d records, more=%v, %v", len(page.Records), page.More, err)
	}
}
