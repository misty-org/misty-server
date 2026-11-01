package browsersync

import (
	"context"
	"errors"
	"testing"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

// A closing account is read-only: its devices still pull what the vault holds,
// and every write is refused as account_closing until it is resumed.
func TestClosingAccountSyncReadsButRefusesWrites(t *testing.T) {
	store, vault, d := setupWorkspaceVault(t)
	ctx := context.Background()
	key := recordKey("c")
	if _, _, err := store.PushBrowserSyncRecords(ctx, "owner", vault, d[0].grant.DeviceID, "bookmarks", []SyncRecordWrite{{Key: key, Ciphertext: []byte("kept")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Conn.Exec(`INSERT INTO account_controls(user_id,delete_at) VALUES('owner',now()+interval '30 days')`); err != nil {
		t.Fatal(err)
	}

	_, _, err := store.PushBrowserSyncRecords(ctx, "owner", vault, d[0].grant.DeviceID, "bookmarks", []SyncRecordWrite{{Key: key, BaseVersion: 1, Ciphertext: []byte("changed")}})
	if !errors.Is(err, db.ErrAccountClosing) {
		t.Fatalf("records push while closing: %v", err)
	}
	if code, status := syncErrorCode(err); code != "account_closing" || status != 403 {
		t.Fatalf("closing answer = %s %d", code, status)
	}
	hash := make([]byte, 32)
	hash[0] = 9
	if err := store.PutBrowserSyncBlob(ctx, "owner", vault, d[0].grant.DeviceID, SyncBlob{Hash: hash, Ciphertext: workspaceCipher()}); !errors.Is(err, db.ErrAccountClosing) {
		t.Fatalf("blob put while closing: %v", err)
	}
	page, err := store.PullBrowserSyncRecords(ctx, "owner", vault, d[1].grant.DeviceID, "bookmarks", 0, 0)
	if err != nil || len(page.Records) != 1 || string(page.Records[0].Ciphertext) != "kept" {
		t.Fatalf("pull while closing: %+v %v", page, err)
	}

	if _, err := store.Conn.Exec(`UPDATE account_controls SET delete_at=NULL WHERE user_id='owner'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.PushBrowserSyncRecords(ctx, "owner", vault, d[0].grant.DeviceID, "bookmarks", []SyncRecordWrite{{Key: key, BaseVersion: 1, Ciphertext: []byte("changed")}}); err != nil {
		t.Fatalf("records push after resume: %v", err)
	}
}
