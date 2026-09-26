package browsersync

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"testing"
)

func TestBrowserSyncRustConnectionFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/browser-sync-protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		WorkspaceID string `json:"workspace_id"`
		Challenge   string `json:"challenge"`
		Grant       struct {
			DeviceID  string `json:"device_id"`
			PublicKey []byte `json:"public_key"`
		} `json:"grant"`
		ConnectionSigningBytes string `json:"connection_signing_bytes"`
		ConnectionSignature    []byte `json:"connection_signature"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	proof := syncConnectionProof(f.WorkspaceID, f.Grant.DeviceID, f.Challenge)
	if string(proof) != f.ConnectionSigningBytes || !ed25519.Verify(f.Grant.PublicKey, proof, f.ConnectionSignature) {
		t.Fatal("Rust connection proof does not match the Go protocol")
	}
}
