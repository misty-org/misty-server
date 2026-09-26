package browsersync

import (
	"encoding/json"
	"os"
	"testing"
)

// Shared with src-tauri/crates/browser-sync/tests/fixtures/tree-v2.json: the
// Rust encoder asserts the same bytes, so a wire change must pass both.
func TestBrowserSyncTreeSigningFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/browser-sync-tree-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		WorkspaceID       string           `json:"workspace_id"`
		TreeID            string           `json:"tree_id"`
		OperationID       string           `json:"operation_id"`
		DeviceID          string           `json:"device_id"`
		DeviceCounter     int64            `json:"device_counter"`
		KeyEpoch          int64            `json:"key_epoch"`
		BaseTreeVersion   int64            `json:"base_tree_version"`
		MerkleRoot        []byte           `json:"merkle_root"`
		Manifest          SyncTreeManifest `json:"manifest"`
		OpSigningBytes    string           `json:"op_signing_bytes"`
		Claim             SyncTreeClaim    `json:"claim"`
		ClaimSigningBytes string           `json:"claim_signing_bytes"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	got := syncTreeSigningBytes(f.WorkspaceID, f.TreeID, f.OperationID, f.DeviceID, f.DeviceCounter, f.KeyEpoch, f.BaseTreeVersion, f.MerkleRoot, f.Manifest)
	if string(got) != f.OpSigningBytes {
		t.Fatalf("tree op signing bytes diverged from the Rust encoder:\n%s\n%s", got, f.OpSigningBytes)
	}
	if string(f.Claim.SigningBytes()) != f.ClaimSigningBytes {
		t.Fatalf("claim signing bytes diverged:\n%s\n%s", f.Claim.SigningBytes(), f.ClaimSigningBytes)
	}
}
