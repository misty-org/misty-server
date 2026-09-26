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
	ErrSyncTreeVersion   = errors.New("sync tree version changed")
	ErrSyncTreeNotDriver = errors.New("sync tree driven by another device")
	ErrSyncTreeMode      = errors.New("sync tree protocol not enabled for this workspace")
	ErrSyncTreeSnapshot  = errors.New("sync tree snapshot required")
)

const (
	SyncTreeMaxNodeBytes  = 64 << 10
	SyncTreeMaxSlotBytes  = 1 << 20
	SyncTreeMaxBlobBytes  = 256 << 10
	SyncTreeMaxOpEntries  = 512
	SyncTreeMaxOpBytes    = 1400 << 10
	SyncTreeMaxSlotKind   = 16
	syncAEADMinCiphertext = 28 // 12-byte nonce + 16-byte tag
)

// Slot kinds are protocol constants shared with native clients.
const (
	SyncSlotHistory        int16 = 1
	SyncSlotPageState      int16 = 2
	SyncSlotSessionStorage int16 = 3
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
	TabNodeID  string `json:"tab_node_id"`
	Slot       int16  `json:"slot"`
	Ciphertext []byte `json:"ciphertext"` // nil deletes the slot
}

// SyncTreeOp replaces a subset of one tree's nodes and slots, compare-and-swap
// on the whole tree version. Every op rewrites the tree's root node (node_id
// = tree_id), which carries the Merkle root clients verify against.
type SyncTreeOp struct {
	WorkspaceID     string          `json:"workspace_id"`
	TreeID          string          `json:"tree_id"`
	OperationID     string          `json:"operation_id"`
	DeviceID        string          `json:"device_id"`
	DeviceCounter   int64           `json:"device_counter"`
	KeyEpoch        int64           `json:"key_epoch"`
	BaseTreeVersion int64           `json:"base_tree_version"`
	MerkleRoot      []byte          `json:"merkle_root"`
	Upserts         []SyncNodeWrite `json:"upserts"`
	Slots           []SyncSlotWrite `json:"slots"`
	Deletes         []string        `json:"deletes"`
	BlobRefs        [][]byte        `json:"blob_refs"`
	Signature       []byte          `json:"signature"`
}

func (op SyncTreeOp) TreeVersion() int64 { return op.BaseTreeVersion + 1 }

// Manifest is exactly what is signed besides the header, and what the change
// feed stores, so any client can re-verify attribution without the content.
type SyncTreeManifest struct {
	Upserts  [][3]string `json:"upserts"` // node_id, parent_id ("" for root), base64(sha256(ciphertext))
	Slots    [][3]string `json:"slots"`   // tab_node_id, slot, base64 hash ("" = deleted)
	Deletes  []string    `json:"deletes"`
	BlobRefs []string    `json:"blob_refs"`
}

func syncHash(data []byte) string {
	sum := sha256.Sum256(data)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (op SyncTreeOp) Manifest() SyncTreeManifest {
	m := SyncTreeManifest{Upserts: [][3]string{}, Slots: [][3]string{}, Deletes: []string{}, BlobRefs: []string{}}
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
		m.Slots = append(m.Slots, [3]string{s.TabNodeID, itoa16(s.Slot), hash})
	}
	m.Deletes = append(m.Deletes, op.Deletes...)
	for _, b := range op.BlobRefs {
		m.BlobRefs = append(m.BlobRefs, base64.StdEncoding.EncodeToString(b))
	}
	return m
}

func itoa16(v int16) string { return strconv.Itoa(int(v)) }

func syncTreeSigningBytes(workspace, tree, operation, device string, counter, keyEpoch, base int64, merkle []byte, manifest SyncTreeManifest) []byte {
	data, _ := json.Marshal([]any{"misty.sync.tree-op.v2", workspace, tree, operation, device, counter, keyEpoch, base, base + 1, base64.StdEncoding.EncodeToString(merkle), manifest.Upserts, manifest.Slots, manifest.Deletes, manifest.BlobRefs})
	return data
}

func (op SyncTreeOp) SigningBytes() []byte {
	return syncTreeSigningBytes(op.WorkspaceID, op.TreeID, op.OperationID, op.DeviceID, op.DeviceCounter, op.KeyEpoch, op.BaseTreeVersion, op.MerkleRoot, op.Manifest())
}

func validCiphertext(data []byte, limit int) bool {
	return len(data) >= syncAEADMinCiphertext && len(data) <= limit
}

// Valid checks structure only; authorization and CAS happen in the store.
func (op SyncTreeOp) Valid() bool {
	if !validSyncID(op.WorkspaceID) || !validSyncID(op.TreeID) || !validSyncID(op.OperationID) || !validSyncID(op.DeviceID) ||
		op.DeviceCounter <= 0 || op.DeviceCounter > SyncMaxCounter || op.KeyEpoch <= 0 || op.KeyEpoch > SyncMaxCounter ||
		op.BaseTreeVersion < 0 || op.BaseTreeVersion >= SyncMaxCounter || len(op.MerkleRoot) != 32 || len(op.Signature) != ed25519.SignatureSize {
		return false
	}
	if len(op.Upserts)+len(op.Slots)+len(op.Deletes)+len(op.BlobRefs) > SyncTreeMaxOpEntries {
		return false
	}
	seen := map[string]bool{}
	total, root := 0, false
	for _, n := range op.Upserts {
		if !validSyncID(n.NodeID) || seen[n.NodeID] || !validCiphertext(n.Ciphertext, SyncTreeMaxNodeBytes) {
			return false
		}
		seen[n.NodeID] = true
		if n.NodeID == op.TreeID {
			if n.ParentID != nil {
				return false
			}
			root = true
		} else if n.ParentID == nil || !validSyncID(*n.ParentID) || *n.ParentID == n.NodeID {
			return false
		}
		total += len(n.Ciphertext)
	}
	// The root always changes: it carries the new Merkle root and tree version.
	if !root {
		return false
	}
	for _, id := range op.Deletes {
		if !validSyncID(id) || seen[id] || id == op.TreeID {
			return false
		}
		seen[id] = true
	}
	slots := map[[2]string]bool{}
	for _, s := range op.Slots {
		key := [2]string{s.TabNodeID, itoa16(s.Slot)}
		if !validSyncID(s.TabNodeID) || s.Slot < 1 || s.Slot > SyncTreeMaxSlotKind || slots[key] || (s.Ciphertext != nil && !validCiphertext(s.Ciphertext, SyncTreeMaxSlotBytes)) {
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
	return total <= SyncTreeMaxOpBytes
}

// SyncTreeClaim moves the sender's single driver seat onto a tree. A claim
// whose operation_id matches a remote control request must still be current.
type SyncTreeClaim struct {
	WorkspaceID   string `json:"workspace_id"`
	TreeID        string `json:"tree_id"`
	OperationID   string `json:"operation_id"`
	DeviceID      string `json:"device_id"`
	DeviceCounter int64  `json:"device_counter"`
	KeyEpoch      int64  `json:"key_epoch"`
	Signature     []byte `json:"signature"`
}

func (c SyncTreeClaim) SigningBytes() []byte {
	data, _ := json.Marshal([]any{"misty.sync.tree-claim.v2", c.WorkspaceID, c.TreeID, c.OperationID, c.DeviceID, c.DeviceCounter, c.KeyEpoch})
	return data
}
func (c SyncTreeClaim) Valid() bool {
	return validSyncID(c.WorkspaceID) && validSyncID(c.TreeID) && c.TreeID != c.WorkspaceID && validSyncID(c.OperationID) && validSyncID(c.DeviceID) &&
		c.DeviceCounter > 0 && c.DeviceCounter <= SyncMaxCounter && c.KeyEpoch > 0 && c.KeyEpoch <= SyncMaxCounter && len(c.Signature) == ed25519.SignatureSize
}

type SyncTree struct {
	TreeID         string  `json:"tree_id"`
	Shared         bool    `json:"shared"`
	DriverDeviceID *string `json:"driver_device_id"`
	DriverEpoch    *string `json:"driver_epoch"`
	DriverSeenAt   *int64  `json:"driver_seen_at"`
	Version        int64   `json:"version"`
}
type SyncTreeNode struct {
	NodeID     string  `json:"node_id"`
	ParentID   *string `json:"parent_id"`
	Version    int64   `json:"version"`
	KeyEpoch   int64   `json:"key_epoch"`
	Ciphertext []byte  `json:"ciphertext"`
}

// Slot metadata travels with snapshots so clients can verify the Merkle root
// without downloading slot content, which stays lazy.
type SyncSlotMeta struct {
	TabNodeID   string `json:"tab_node_id"`
	Slot        int16  `json:"slot"`
	Version     int64  `json:"version"`
	ContentHash []byte `json:"content_hash"`
}
type SyncTreeChange struct {
	TreeVersion   int64            `json:"tree_version"`
	Sequence      int64            `json:"sequence"`
	OperationID   string           `json:"operation_id"`
	DeviceID      string           `json:"device_id"`
	DeviceCounter int64            `json:"device_counter"`
	KeyEpoch      int64            `json:"key_epoch"`
	MerkleRoot    []byte           `json:"merkle_root"`
	Manifest      SyncTreeManifest `json:"manifest"`
	Signature     []byte           `json:"signature"`
}

// SyncTreeDelta carries every signed manifest after a client's version plus
// the current content of each node they touched. Touched slot keys missing
// from Slots no longer exist.
type SyncTreeDelta struct {
	TreeID  string           `json:"tree_id"`
	Version int64            `json:"version"`
	Changes []SyncTreeChange `json:"changes"`
	Nodes   []SyncTreeNode   `json:"nodes"`
	Slots   []SyncSlotMeta   `json:"slots"`
	Deleted []string         `json:"deleted"`
}
type SyncTreeSnapshot struct {
	TreeID     string          `json:"tree_id"`
	Version    int64           `json:"version"`
	Nodes      []SyncTreeNode  `json:"nodes"`
	Slots      []SyncSlotMeta  `json:"slots"`
	LastChange *SyncTreeChange `json:"last_change"`
}
