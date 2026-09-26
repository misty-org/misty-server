package browsersync

import (
	"container/list"
	"context"
	"sync"
)

// treeCache keeps recently read tree snapshots in memory. An entry is served
// only while its version equals the tree's committed version, so a write on
// any server instance invalidates it without cross-instance messaging.
type treeCache struct {
	mu      sync.Mutex
	limit   int
	size    int
	order   *list.List
	entries map[[2]string]*list.Element
}

type treeCacheEntry struct {
	key      [2]string
	snapshot *SyncTreeSnapshot
	bytes    int
}

func newTreeCache(limit int) *treeCache {
	return &treeCache{limit: limit, order: list.New(), entries: map[[2]string]*list.Element{}}
}

func snapshotBytes(s *SyncTreeSnapshot) int {
	n := 256
	for _, node := range s.Nodes {
		n += len(node.Ciphertext) + 96
	}
	return n + len(s.Slots)*96
}

func (c *treeCache) get(workspace, tree string, version int64) *SyncTreeSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[[2]string{workspace, tree}]
	if !ok {
		return nil
	}
	entry := el.Value.(*treeCacheEntry)
	if entry.snapshot.Version != version {
		c.remove(el)
		return nil
	}
	c.order.MoveToFront(el)
	return entry.snapshot
}

func (c *treeCache) put(workspace string, s *SyncTreeSnapshot) {
	bytes := snapshotBytes(s)
	if bytes > c.limit/8 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := [2]string{workspace, s.TreeID}
	if el, ok := c.entries[key]; ok {
		c.remove(el)
	}
	c.entries[key] = c.order.PushFront(&treeCacheEntry{key: key, snapshot: s, bytes: bytes})
	c.size += bytes
	for c.size > c.limit {
		c.remove(c.order.Back())
	}
}

func (c *treeCache) remove(el *list.Element) {
	entry := el.Value.(*treeCacheEntry)
	c.order.Remove(el)
	delete(c.entries, entry.key)
	c.size -= entry.bytes
}

// treeSnapshot authorizes the device, then serves a current cached snapshot
// or loads and caches one. Snapshots are immutable once cached.
func (s *BrowserSyncService) treeSnapshot(ctx context.Context, identity SyncConnectionIdentity, tree string) (*SyncTreeSnapshot, error) {
	tx, err := s.store.readTx(ctx, identity.UserID, identity.WorkspaceID, identity.DeviceID)
	if err != nil {
		return nil, err
	}
	version, err := treeVersion(ctx, tx, identity.WorkspaceID, tree)
	tx.Rollback()
	if err != nil {
		return nil, err
	}
	if cached := s.trees.get(identity.WorkspaceID, tree, version); cached != nil {
		return cached, nil
	}
	snapshot, err := s.store.BrowserSyncTreeSnapshot(ctx, identity.UserID, identity.WorkspaceID, identity.DeviceID, tree)
	if err != nil {
		return nil, err
	}
	s.trees.put(identity.WorkspaceID, snapshot)
	return snapshot, nil
}
