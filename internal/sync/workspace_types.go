package browsersync

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
)

var (
	ErrSyncWorkspaceMode     = errors.New("sync workspace protocol not enabled for this vault")
	ErrSyncWorkspaceSnapshot = errors.New("sync workspace snapshot required")
)

// syncWorkspaceProtocol is the socket protocol for workspaces. Version 3
// renamed trees to workspaces and the sync workspace to the vault on the
// wire; version 2 clients speak the old names and are turned away.
const syncWorkspaceProtocol = 3

const (
	SyncWorkspaceMaxNodeBytes = 64 << 10
	SyncWorkspaceMaxSlotBytes = 1 << 20
	SyncWorkspaceMaxBlobBytes = 256 << 10
	SyncWorkspaceMaxOpEntries = 512
	SyncWorkspaceMaxOpBytes   = 1400 << 10
	SyncWorkspaceMaxSlotKind  = 16
	syncAEADMinCiphertext     = 28 // 12-byte nonce + 16-byte tag
)

// Slot kinds are protocol constants shared with native clients.
const (
	SyncSlotHistory   int16 = 1
	SyncSlotPageState int16 = 2
	// A device's website sign-in data lives in these slots on its workspace's root
	// node. Its lease holder (the workspace's driver) is the only one who may write it.
	SyncSlotSigninFirst int16 = 4
	SyncSlotSigninLast  int16 = SyncWorkspaceMaxSlotKind
)

// Ciphertexts are nonce||AES-256-GCM output. Clients bind each node to its
// position with associated data, so the server cannot move or roll it back
// without clients detecting it; the server only checks shape and size.
type SyncNodeWrite struct {
	NodeID     string  `json:"node_id"`
	ParentID   *string `json:"parent_id"`
	Ciphertext []byte  `json:"ciphertext"`
}
type SyncSlotWrite struct {
	ViewNodeID string `json:"view_node_id"`
	Slot       int16  `json:"slot"`
	Ciphertext []byte `json:"ciphertext"` // nil deletes the slot
}

// SyncWorkspaceOp replaces a subset of one workspace's nodes and slots, compare-and-swap
// on the whole workspace version. Every op rewrites the workspace's root node (node_id
// = workspace_id), which carries the Merkle root clients verify against.
type SyncWorkspaceOp struct {
	VaultID              string          `json:"vault_id"`
	WorkspaceID          string          `json:"workspace_id"`
	OperationID          string          `json:"operation_id"`
	DeviceID             string          `json:"device_id"`
	DeviceCounter        int64           `json:"device_counter"`
	KeyEpoch             int64           `json:"key_epoch"`
	BaseWorkspaceVersion int64           `json:"base_workspace_version"`
	MerkleRoot           []byte          `json:"merkle_root"`
	Upserts              []SyncNodeWrite `json:"upserts"`
	Slots                []SyncSlotWrite `json:"slots"`
	Deletes              []string        `json:"deletes"`
	BlobRefs             [][]byte        `json:"blob_refs"`
	Signature            []byte          `json:"signature"`
}

func (op SyncWorkspaceOp) WorkspaceVersion() int64 { return op.BaseWorkspaceVersion + 1 }

// writesSignin reports whether the op touches the device's sign-in data,
// which only its lease holder may write.
func (op SyncWorkspaceOp) writesSignin() bool {
	for _, slot := range op.Slots {
		if slot.ViewNodeID == op.WorkspaceID && slot.Slot >= SyncSlotSigninFirst && slot.Slot <= SyncSlotSigninLast {
			return true
		}
	}
	return false
}

// Manifest is exactly what is signed besides the header, and what the change
// feed stores, so any client can re-verify attribution without the content.
type SyncWorkspaceManifest struct {
	Upserts  [][3]string `json:"upserts"` // node_id, parent_id ("" for root), base64(sha256(ciphertext))
	Slots    [][3]string `json:"slots"`   // view_node_id, slot, base64 hash ("" = deleted)
	Deletes  []string    `json:"deletes"`
	BlobRefs []string    `json:"blob_refs"`
}

func syncHash(data []byte) string {
	sum := sha256.Sum256(data)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (op SyncWorkspaceOp) Manifest() SyncWorkspaceManifest {
	m := SyncWorkspaceManifest{Upserts: [][3]string{}, Slots: [][3]string{}, Deletes: []string{}, BlobRefs: []string{}}
	for _, n := range op.Upserts {
		parent := ""
		if n.ParentID != nil {
			parent = *n.ParentID
		}
		m.Upserts = append(m.Upserts, [3]string{n.NodeID, parent, syncHash(n.Ciphertext)})
	}
	for _, s := range op.Slots {
		hash := ""
		if s.Ciphertext != nil {
			hash = syncHash(s.Ciphertext)
		}
		m.Slots = append(m.Slots, [3]string{s.ViewNodeID, itoa16(s.Slot), hash})
	}
	m.Deletes = append(m.Deletes, op.Deletes...)
	for _, b := range op.BlobRefs {
		m.BlobRefs = append(m.BlobRefs, base64.StdEncoding.EncodeToString(b))
	}
	return m
}

func itoa16(v int16) string { return strconv.Itoa(int(v)) }

func syncWorkspaceSigningBytes(vault, workspace, operation, device string, counter, keyEpoch, base int64, merkle []byte, manifest SyncWorkspaceManifest) []byte {
	data, _ := json.Marshal([]any{"misty.sync.tree-op.v2", vault, workspace, operation, device, counter, keyEpoch, base, base + 1, base64.StdEncoding.EncodeToString(merkle), manifest.Upserts, manifest.Slots, manifest.Deletes, manifest.BlobRefs})
	return data
}

func (op SyncWorkspaceOp) SigningBytes() []byte {
	return syncWorkspaceSigningBytes(op.VaultID, op.WorkspaceID, op.OperationID, op.DeviceID, op.DeviceCounter, op.KeyEpoch, op.BaseWorkspaceVersion, op.MerkleRoot, op.Manifest())
}

func validCiphertext(data []byte, limit int) bool {
	return len(data) >= syncAEADMinCiphertext && len(data) <= limit
}

// Valid checks structure only; authorization and CAS happen in the store.
func (op SyncWorkspaceOp) Valid() bool {
	if !validSyncID(op.VaultID) || !validSyncID(op.WorkspaceID) || !validSyncID(op.OperationID) || !validSyncID(op.DeviceID) ||
		op.DeviceCounter <= 0 || op.DeviceCounter > SyncMaxCounter || op.KeyEpoch <= 0 || op.KeyEpoch > SyncMaxCounter ||
		op.BaseWorkspaceVersion < 0 || op.BaseWorkspaceVersion >= SyncMaxCounter || len(op.MerkleRoot) != 32 || len(op.Signature) != ed25519.SignatureSize {
		return false
	}
	if len(op.Upserts)+len(op.Slots)+len(op.Deletes)+len(op.BlobRefs) > SyncWorkspaceMaxOpEntries {
		return false
	}
	seen := map[string]bool{}
	total, root := 0, false
	for _, n := range op.Upserts {
		if !validSyncID(n.NodeID) || seen[n.NodeID] || !validCiphertext(n.Ciphertext, SyncWorkspaceMaxNodeBytes) {
			return false
		}
		seen[n.NodeID] = true
		if n.NodeID == op.WorkspaceID {
			if n.ParentID != nil {
				return false
			}
			root = true
		} else if n.ParentID == nil || !validSyncID(*n.ParentID) || *n.ParentID == n.NodeID {
			return false
		}
		total += len(n.Ciphertext)
	}
	// The root always changes: it carries the new Merkle root and workspace version.
	if !root {
		return false
	}
	for _, id := range op.Deletes {
		if !validSyncID(id) || seen[id] || id == op.WorkspaceID {
			return false
		}
		seen[id] = true
	}
	slots := map[[2]string]bool{}
	for _, s := range op.Slots {
		key := [2]string{s.ViewNodeID, itoa16(s.Slot)}
		if !validSyncID(s.ViewNodeID) || s.Slot < 1 || s.Slot > SyncWorkspaceMaxSlotKind || slots[key] || (s.Ciphertext != nil && !validCiphertext(s.Ciphertext, SyncWorkspaceMaxSlotBytes)) {
			return false
		}
		slots[key] = true
		total += len(s.Ciphertext)
	}
	for _, b := range op.BlobRefs {
		if len(b) != 32 {
			return false
		}
	}
	return total <= SyncWorkspaceMaxOpBytes
}

// SyncWorkspaceClaim moves the sender's single driver seat onto a workspace. A claim
// whose operation_id matches a remote control request must still be current.
type SyncWorkspaceClaim struct {
	VaultID       string `json:"vault_id"`
	WorkspaceID   string `json:"workspace_id"`
	OperationID   string `json:"operation_id"`
	DeviceID      string `json:"device_id"`
	DeviceCounter int64  `json:"device_counter"`
	KeyEpoch      int64  `json:"key_epoch"`
	Signature     []byte `json:"signature"`
}

func (c SyncWorkspaceClaim) SigningBytes() []byte {
	data, _ := json.Marshal([]any{"misty.sync.tree-claim.v2", c.VaultID, c.WorkspaceID, c.OperationID, c.DeviceID, c.DeviceCounter, c.KeyEpoch})
	return data
}
func (c SyncWorkspaceClaim) Valid() bool {
	return validSyncID(c.VaultID) && validSyncID(c.WorkspaceID) && c.WorkspaceID != c.VaultID && validSyncID(c.OperationID) && validSyncID(c.DeviceID) &&
		c.DeviceCounter > 0 && c.DeviceCounter <= SyncMaxCounter && c.KeyEpoch > 0 && c.KeyEpoch <= SyncMaxCounter && len(c.Signature) == ed25519.SignatureSize
}

type SyncWorkspace struct {
	WorkspaceID    string  `json:"workspace_id"`
	Shared         bool    `json:"shared"`
	DriverDeviceID *string `json:"driver_device_id"`
	DriverEpoch    *string `json:"driver_epoch"`
	DriverSeenAt   *int64  `json:"driver_seen_at"`
	Version        int64   `json:"version"`
}
type SyncWorkspaceNode struct {
	NodeID     string  `json:"node_id"`
	ParentID   *string `json:"parent_id"`
	Version    int64   `json:"version"`
	KeyEpoch   int64   `json:"key_epoch"`
	Ciphertext []byte  `json:"ciphertext"`
}

// Slot metadata travels with snapshots so clients can verify the Merkle root
// without downloading slot content, which stays lazy.
type SyncSlotMeta struct {
	ViewNodeID  string `json:"view_node_id"`
	Slot        int16  `json:"slot"`
	Version     int64  `json:"version"`
	ContentHash []byte `json:"content_hash"`
}
type SyncWorkspaceChange struct {
	WorkspaceVersion int64                 `json:"workspace_version"`
	Sequence         int64                 `json:"sequence"`
	OperationID      string                `json:"operation_id"`
	DeviceID         string                `json:"device_id"`
	DeviceCounter    int64                 `json:"device_counter"`
	KeyEpoch         int64                 `json:"key_epoch"`
	MerkleRoot       []byte                `json:"merkle_root"`
	Manifest         SyncWorkspaceManifest `json:"manifest"`
	Signature        []byte                `json:"signature"`
}

// SyncWorkspaceDelta carries every signed manifest after a client's version plus
// the current content of each node they touched. Touched slot keys missing
// from Slots no longer exist.
type SyncWorkspaceDelta struct {
	WorkspaceID string                `json:"workspace_id"`
	Version     int64                 `json:"version"`
	Changes     []SyncWorkspaceChange `json:"changes"`
	Nodes       []SyncWorkspaceNode   `json:"nodes"`
	Slots       []SyncSlotMeta        `json:"slots"`
	Deleted     []string              `json:"deleted"`
}
type SyncWorkspaceSnapshot struct {
	WorkspaceID string               `json:"workspace_id"`
	Version     int64                `json:"version"`
	Nodes       []SyncWorkspaceNode  `json:"nodes"`
	Slots       []SyncSlotMeta       `json:"slots"`
	LastChange  *SyncWorkspaceChange `json:"last_change"`
}
