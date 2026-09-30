package browsersync

import (
	"container/list"
	"context"
	"strconv"
	"sync"
)

// versionedCache keeps recently read workspace data in memory, bounded by bytes
// and evicted least recently used first. An entry is served only while its
// version equals the workspace's committed version, so a write on any server
// instance invalidates it without cross-instance messaging. Entries are
// immutable once cached.
type versionedCache[V any] struct {
	mu      sync.Mutex
	limit   int
	size    int
	sizeOf  func(V) int
	order   *list.List
	entries map[string]*list.Element
}

type versionedEntry[V any] struct {
	key     string
	version int64
	value   V
	bytes   int
}

func newVersionedCache[V any](limit int, sizeOf func(V) int) *versionedCache[V] {
	return &versionedCache[V]{limit: limit, sizeOf: sizeOf, order: list.New(), entries: map[string]*list.Element{}}
}

func (c *versionedCache[V]) get(key string, version int64) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	el, ok := c.entries[key]
	if !ok {
		return zero, false
	}
	entry := el.Value.(*versionedEntry[V])
	if entry.version != version {
		c.remove(el)
		return zero, false
	}
	c.order.MoveToFront(el)
	return entry.value, true
}

func (c *versionedCache[V]) put(key string, version int64, value V) {
	bytes := c.sizeOf(value)
	if bytes > c.limit/8 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.remove(el)
	}
	c.entries[key] = c.order.PushFront(&versionedEntry[V]{key: key, version: version, value: value, bytes: bytes})
	c.size += bytes
	for c.size > c.limit {
		c.remove(c.order.Back())
	}
}

func (c *versionedCache[V]) remove(el *list.Element) {
	entry := el.Value.(*versionedEntry[V])
	c.order.Remove(el)
	delete(c.entries, entry.key)
	c.size -= entry.bytes
}

// workspaceCaches holds the hot tier: snapshots of active workspaces, and the deltas
// every machine watching a workspace asks for after each edit to it.
type workspaceCaches struct {
	snapshots *versionedCache[*SyncWorkspaceSnapshot]
	deltas    *versionedCache[*SyncWorkspaceDelta]
}

func newWorkspaceCaches(limit int) workspaceCaches {
	return workspaceCaches{
		snapshots: newVersionedCache(limit*3/4, snapshotBytes),
		deltas:    newVersionedCache(limit/4, deltaBytes),
	}
}

func snapshotBytes(s *SyncWorkspaceSnapshot) int {
	n := 256
	for _, node := range s.Nodes {
		n += len(node.Ciphertext) + 96
	}
	return n + len(s.Slots)*96
}

func deltaBytes(d *SyncWorkspaceDelta) int {
	n := 256 + len(d.Changes)*512 + len(d.Slots)*96 + len(d.Deleted)*40
	for _, node := range d.Nodes {
		n += len(node.Ciphertext) + 96
	}
	return n
}

func (s *BrowserSyncService) committedVersion(ctx context.Context, identity SyncConnectionIdentity, workspace string) (int64, error) {
	tx, err := s.store.readTx(ctx, identity.UserID, identity.VaultID, identity.DeviceID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	return workspaceVersion(ctx, tx, identity.VaultID, workspace)
}

// workspaceSnapshot authorizes the device, then serves a current cached snapshot
// or loads and caches one.
func (s *BrowserSyncService) workspaceSnapshot(ctx context.Context, identity SyncConnectionIdentity, workspace string) (*SyncWorkspaceSnapshot, error) {
	version, err := s.committedVersion(ctx, identity, workspace)
	if err != nil {
		return nil, err
	}
	key := identity.VaultID + "/" + workspace
	if cached, ok := s.workspaces.snapshots.get(key, version); ok {
		return cached, nil
	}
	snapshot, err := s.store.BrowserSyncWorkspaceSnapshot(ctx, identity.UserID, identity.VaultID, identity.DeviceID, workspace)
	if err != nil {
		return nil, err
	}
	s.workspaces.snapshots.put(key, snapshot.Version, snapshot)
	return snapshot, nil
}

// workspaceDelta authorizes the device, then serves the delta from `after` to the
// committed version from memory when another watcher already read it.
func (s *BrowserSyncService) workspaceDelta(ctx context.Context, identity SyncConnectionIdentity, workspace string, after int64) (*SyncWorkspaceDelta, error) {
	version, err := s.committedVersion(ctx, identity, workspace)
	if err != nil {
		return nil, err
	}
	if after >= version {
		// Current, or an invalid cursor: the store answers both precisely.
		return s.store.BrowserSyncWorkspaceDelta(ctx, identity.UserID, identity.VaultID, identity.DeviceID, workspace, after)
	}
	key := identity.VaultID + "/" + workspace + "/" + strconv.FormatInt(after, 10)
	if cached, ok := s.workspaces.deltas.get(key, version); ok {
		return cached, nil
	}
	delta, err := s.store.BrowserSyncWorkspaceDelta(ctx, identity.UserID, identity.VaultID, identity.DeviceID, workspace, after)
	if err != nil {
		return nil, err
	}
	s.workspaces.deltas.put(key, delta.Version, delta)
	return delta, nil
}
