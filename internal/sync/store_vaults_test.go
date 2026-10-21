package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestBrowserSyncVaultPerSpace(t *testing.T) {
	database, _ := syncTestDatabase(t)
	ctx := context.Background()
	if _, err := database.Conn.Exec(`INSERT INTO spaces(id,owner_user_id) VALUES('shared','other'),('private','other');
		INSERT INTO space_members(space_id,user_id,role) VALUES('shared','other','owner'),('shared','owner','member'),('private','other','owner')`); err != nil {
		t.Fatal(err)
	}
	personalRoot, personalKey, _ := ed25519.GenerateKey(rand.Reader)
	device, deviceKey := syncTestGrant(uuid.NewString(), personalKey)
	if err := database.CreateBrowserSyncVault(ctx, "owner", "", personalRoot, PasswordVaultKeys(syncTestKeyEnvelope()), device); err != nil {
		t.Fatal(err)
	}

	// The same device grants itself into the Space vault under that vault's root.
	spaceRoot, spaceKey, _ := ed25519.GenerateKey(rand.Reader)
	spaceGrant := func(spaceDevice SyncDeviceGrant) SyncDeviceGrant {
		g := SyncDeviceGrant{VaultID: uuid.NewString(), DeviceID: spaceDevice.DeviceID, KeyEpoch: 1, PublicKey: spaceDevice.PublicKey}
		g.Signature = ed25519.Sign(spaceKey, g.SigningBytes())
		return g
	}
	if err := database.CreateBrowserSyncVault(ctx, "owner", "private", spaceRoot, SpaceVaultKeys(syncTestEnvelope()), spaceGrant(device)); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("non-member created a vault: %v", err)
	}
	if err := database.CreateBrowserSyncVault(ctx, "owner", "shared", spaceRoot, PasswordVaultKeys(syncTestKeyEnvelope()), spaceGrant(device)); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("second password-wrapped vault: %v", err)
	}
	if err := database.CreateBrowserSyncVault(ctx, "owner", "personal_owner", spaceRoot, SpaceVaultKeys(syncTestEnvelope()), spaceGrant(device)); !errors.Is(err, ErrSyncInvalid) {
		t.Fatalf("sealed personal vault: %v", err)
	}
	stranger, _ := syncTestGrant(uuid.NewString(), personalKey)
	if err := database.CreateBrowserSyncVault(ctx, "owner", "shared", spaceRoot, SpaceVaultKeys(syncTestEnvelope()), spaceGrant(stranger)); !errors.Is(err, ErrSyncForbidden) {
		t.Fatalf("device outside the personal vault granted a Space vault: %v", err)
	}
	shared := spaceGrant(device)
	if err := database.CreateBrowserSyncVault(ctx, "owner", "shared", spaceRoot, SpaceVaultKeys(syncTestEnvelope()), shared); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateBrowserSyncVault(ctx, "owner", "shared", spaceRoot, SpaceVaultKeys(syncTestEnvelope()), spaceGrant(device)); !errors.Is(err, ErrSyncExists) {
		t.Fatalf("second vault for one Space: %v", err)
	}

	vaults, err := database.BrowserSyncVaults(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(vaults) != 2 || vaults[0].VaultID != device.VaultID || vaults[0].SpaceID != "personal_owner" || vaults[0].KeyEnvelope == nil || vaults[0].SpaceKeyEnvelope != nil ||
		vaults[1].VaultID != shared.VaultID || vaults[1].SpaceID != "shared" || vaults[1].KeyEnvelope != nil || vaults[1].SpaceKeyEnvelope == nil {
		t.Fatalf("vaults: %+v", vaults)
	}
	if personal, err := database.BrowserSyncVault(ctx, "owner"); err != nil || personal == nil || personal.VaultID != device.VaultID {
		t.Fatalf("personal vault: %+v %v", personal, err)
	}
	if v, err := database.BrowserSyncVaultForSpace(ctx, "owner", "shared"); err != nil || v == nil || v.VaultID != shared.VaultID {
		t.Fatalf("vault for Space: %+v %v", v, err)
	}
	if v, err := database.BrowserSyncVaultByID(ctx, "other", shared.VaultID); err != nil || v != nil {
		t.Fatalf("another account read the vault: %+v %v", v, err)
	}

	// The Space vault syncs like any other vault.
	if receipt, err := database.PublishBrowserSync(ctx, "owner", syncTestMutation(shared, deviceKey, 1)); err != nil || receipt.Sequence != 1 {
		t.Fatalf("publish to the Space vault: %+v %v", receipt, err)
	}

	if _, err := database.Conn.Exec(`DELETE FROM spaces WHERE id='shared'`); err != nil {
		t.Fatal(err)
	}
	if vaults, err = database.BrowserSyncVaults(ctx, "owner"); err != nil || len(vaults) != 1 {
		t.Fatalf("vault outlived its Space: %+v %v", vaults, err)
	}

	// Deleting removes every vault, so the account can create a new personal one.
	if _, err := database.Conn.Exec(`INSERT INTO spaces(id,owner_user_id) VALUES('team','other');
		INSERT INTO space_members(space_id,user_id,role) VALUES('team','other','owner'),('team','owner','member')`); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateBrowserSyncVault(ctx, "owner", "team", spaceRoot, SpaceVaultKeys(syncTestEnvelope()), spaceGrant(device)); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateBrowserSyncVault(ctx, "other", "", spaceRoot, PasswordVaultKeys(syncTestKeyEnvelope()), spaceGrant(device)); err != nil {
		t.Fatal(err)
	}
	if deleted, err := database.DeleteBrowserSyncVaults(ctx, "owner"); err != nil || !deleted {
		t.Fatalf("delete vaults: %v %v", deleted, err)
	}
	if vaults, err = database.BrowserSyncVaults(ctx, "owner"); err != nil || len(vaults) != 0 {
		t.Fatalf("vaults outlived deletion: %+v %v", vaults, err)
	}
	if vaults, err = database.BrowserSyncVaults(ctx, "other"); err != nil || len(vaults) != 1 {
		t.Fatalf("deletion reached another account: %+v %v", vaults, err)
	}
	if deleted, err := database.DeleteBrowserSyncVaults(ctx, "owner"); err != nil || deleted {
		t.Fatalf("second delete: %v %v", deleted, err)
	}
	fresh, _ := syncTestGrant(uuid.NewString(), personalKey)
	if err := database.CreateBrowserSyncVault(ctx, "owner", "", personalRoot, PasswordVaultKeys(syncTestKeyEnvelope()), fresh); err != nil {
		t.Fatalf("new vault after delete: %v", err)
	}
}
