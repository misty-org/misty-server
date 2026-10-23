package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/browsersync"
)

func syncVaultTestEnvelope() browsersync.SyncEnvelope {
	return browsersync.SyncEnvelope{Version: 1, Nonce: base64.StdEncoding.EncodeToString(make([]byte, 12)), Ciphertext: base64.StdEncoding.EncodeToString(make([]byte, 48))}
}

func syncVaultTestGrant(vaultID, deviceID string, device ed25519.PublicKey, root ed25519.PrivateKey) browsersync.SyncDeviceGrant {
	g := browsersync.SyncDeviceGrant{VaultID: vaultID, DeviceID: deviceID, KeyEpoch: 1, PublicKey: device}
	g.Signature = ed25519.Sign(root, g.SigningBytes())
	return g
}

// One account holds a vault per Space it belongs to; only the personal vault is
// password-wrapped, and nobody outside a Space can create a vault for it.
func TestSyncVaultsPerSpace(t *testing.T) {
	database := openPresenceTestDatabase(t)
	ctx := context.Background()
	newUser := func(name string) (string, string) {
		user, err := database.CreateUserWithUsername("sv"+strings.ReplaceAll(uuid.NewString()[:13], "-", ""), uniqueTestEmail("sv"), "password123")
		if err != nil {
			t.Fatal(err)
		}
		return user.ID, newConversationTestBearerToken(t, database, user.ID)
	}
	owner, ownerToken := newUser("Vault owner")
	_, strangerToken := newUser("Stranger")

	service := browsersync.NewBrowserSyncService(database)
	router := chi.NewRouter()
	router.Get("/sync/vault", service.Vault())
	router.Post("/sync/vault", service.Vault())
	router.Get("/sync/vaults", service.Vaults())
	router.Post("/sync/vaults", service.Vaults())

	devicePublic, _, _ := ed25519.GenerateKey(rand.Reader)
	deviceID := uuid.NewString()
	personalPublic, personalRoot, _ := ed25519.GenerateKey(rand.Reader)
	personalVault := uuid.NewString()
	e := syncVaultTestEnvelope()
	wrapper := browsersync.SyncKeyEnvelope{Version: 1, KDF: browsersync.SyncKDFAccount, Salt: base64.StdEncoding.EncodeToString(make([]byte, 16)), Nonce: e.Nonce, Ciphertext: e.Ciphertext}
	if rec := performConversationRequest(t, router, "POST", "/sync/vault", ownerToken, map[string]any{
		"root_public_key": []byte(personalPublic), "key_envelope": wrapper, "device": syncVaultTestGrant(personalVault, deviceID, devicePublic, personalRoot),
	}); rec.Code != 201 {
		t.Fatalf("personal vault: %d %s", rec.Code, rec.Body)
	}

	created, err := database.CreateSpace(ctx, owner, "Work", "sync-vaults-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	space := created.Space.ID
	spacePublic, spaceRoot, _ := ed25519.GenerateKey(rand.Reader)
	createSpaceVault := func(token string) (string, int) {
		vaultID := uuid.NewString()
		rec := performConversationRequest(t, router, "POST", "/sync/vaults", token, map[string]any{
			"space_id": space, "root_public_key": []byte(spacePublic), "space_key_envelope": syncVaultTestEnvelope(),
			"device": syncVaultTestGrant(vaultID, deviceID, devicePublic, spaceRoot),
		})
		return vaultID, rec.Code
	}
	if _, code := createSpaceVault(strangerToken); code != 403 {
		t.Fatalf("non-member created a vault: %d", code)
	}
	spaceVault, code := createSpaceVault(ownerToken)
	if code != 201 {
		t.Fatalf("Space vault: %d", code)
	}
	if _, code := createSpaceVault(ownerToken); code != 409 {
		t.Fatalf("second vault for one Space: %d", code)
	}

	rec := performConversationRequest(t, router, "GET", "/sync/vaults", ownerToken, nil)
	var listed struct {
		Vaults []struct {
			SpaceID          string          `json:"space_id"`
			VaultID          string          `json:"vault_id"`
			KeyEnvelope      json.RawMessage `json:"key_envelope"`
			SpaceKeyEnvelope json.RawMessage `json:"space_key_envelope"`
		} `json:"vaults"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Vaults) != 2 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if v := listed.Vaults[0]; v.VaultID != personalVault || v.KeyEnvelope == nil || v.SpaceKeyEnvelope != nil {
		t.Fatalf("personal vault listed as %+v", v)
	}
	if v := listed.Vaults[1]; v.VaultID != spaceVault || v.SpaceID != space || v.KeyEnvelope != nil || v.SpaceKeyEnvelope == nil {
		t.Fatalf("Space vault listed as %+v", v)
	}
	// The single-vault route keeps returning the personal vault.
	if rec := performConversationRequest(t, router, "GET", "/sync/vault", ownerToken, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), personalVault) {
		t.Fatalf("personal vault: %d %s", rec.Code, rec.Body)
	}

	if err := database.DeleteSpace(ctx, owner, space, "Work"); err != nil {
		t.Fatal(err)
	}
	rec = performConversationRequest(t, router, "GET", "/sync/vaults", ownerToken, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Vaults) != 1 {
		t.Fatalf("vault outlived its deleted Space: %d %s", rec.Code, rec.Body)
	}
}
