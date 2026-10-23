package browsersync

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"testing"
)

// This public fixture is produced by the Rust native core, not the Go encoder.
// Any wire-format change must pass both implementations before release.
func TestBrowserSyncRustProtocolFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/browser-sync-protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		RootPublicKey        []byte          `json:"root_public_key"`
		KeyEnvelope          SyncKeyEnvelope `json:"key_envelope"`
		Grant                SyncDeviceGrant `json:"grant"`
		Mutation             SyncMutation    `json:"mutation"`
		GrantSigningBytes    string          `json:"grant_signing_bytes"`
		MutationSigningBytes string          `json:"mutation_signing_bytes"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if !f.KeyEnvelope.Valid() || !f.Grant.valid(f.RootPublicKey) || !f.Mutation.Valid() {
		t.Fatal("native fixture failed protocol validation")
	}
	if string(f.Grant.SigningBytes()) != f.GrantSigningBytes || string(f.Mutation.SigningBytes()) != f.MutationSigningBytes {
		t.Fatal("Rust and Go signature encodings disagree")
	}
	if !ed25519.Verify(f.Grant.PublicKey, f.Mutation.SigningBytes(), f.Mutation.Signature) {
		t.Fatal("Rust mutation signature did not verify")
	}
}
