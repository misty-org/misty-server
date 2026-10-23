package browsersync

import (
	"encoding/json"
	"os"
	"testing"
)

// Shared with src-tauri/crates/browser-sync/tests/fixtures/workspace-v2.json: the
// Rust encoder asserts the same bytes, so a wire change must pass both.
func TestBrowserSyncWorkspaceSigningFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/browser-sync-workspace-v3.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		VaultID              string                `json:"vault_id"`
		WorkspaceID          string                `json:"workspace_id"`
		OperationID          string                `json:"operation_id"`
		DeviceID             string                `json:"device_id"`
		DeviceCounter        int64                 `json:"device_counter"`
		KeyEpoch             int64                 `json:"key_epoch"`
		BaseWorkspaceVersion int64                 `json:"base_workspace_version"`
		MerkleRoot           []byte                `json:"merkle_root"`
		Manifest             SyncWorkspaceManifest `json:"manifest"`
		OpSigningBytes       string                `json:"op_signing_bytes"`
		Claim                SyncWorkspaceClaim    `json:"claim"`
		ClaimSigningBytes    string                `json:"claim_signing_bytes"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	got := syncWorkspaceSigningBytes(f.VaultID, f.WorkspaceID, f.OperationID, f.DeviceID, f.DeviceCounter, f.KeyEpoch, f.BaseWorkspaceVersion, f.MerkleRoot, f.Manifest)
	if string(got) != f.OpSigningBytes {
		t.Fatalf("workspace op signing bytes diverged from the Rust encoder:\n%s\n%s", got, f.OpSigningBytes)
	}
	if string(f.Claim.SigningBytes()) != f.ClaimSigningBytes {
		t.Fatalf("claim signing bytes diverged:\n%s\n%s", f.Claim.SigningBytes(), f.ClaimSigningBytes)
	}
}
