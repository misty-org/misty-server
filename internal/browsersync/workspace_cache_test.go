package browsersync

import "testing"

func TestVersionedCacheEvictsByBytesAndVersion(t *testing.T) {
	sized := func(n int) int { return n }
	c := newVersionedCache(100, sized)
	c.put("a", 1, 10)
	c.put("b", 1, 10)
	if _, ok := c.get("a", 1); !ok {
		t.Fatal("a fresh entry is served")
	}
	// A write on any instance bumps the version: the stale entry is dropped.
	if _, ok := c.get("a", 2); ok {
		t.Fatal("an entry for an older version was served")
	}
	if _, ok := c.get("a", 1); ok {
		t.Fatal("a stale entry stayed cached")
	}
	// Over the byte limit, the least recently used entry goes first.
	for i, key := range []string{"c", "d", "e", "f", "g", "h", "i", "j", "k"} {
		c.put(key, 1, 11+i%2)
	}
	if _, ok := c.get("b", 1); ok {
		t.Fatal("the least recently used entry survived the limit")
	}
	if c.size > c.limit {
		t.Fatalf("cache over its limit: %d > %d", c.size, c.limit)
	}
	// One entry larger than an eighth of the cache is never held.
	c.put("huge", 1, 50)
	if _, ok := c.get("huge", 1); ok {
		t.Fatal("an oversized entry was cached")
	}
}

func TestWorkspaceCachesKeepDeltasPerCursor(t *testing.T) {
	caches := newWorkspaceCaches(1 << 20)
	from1 := &SyncWorkspaceDelta{WorkspaceID: "t", Version: 3}
	from2 := &SyncWorkspaceDelta{WorkspaceID: "t", Version: 3}
	caches.deltas.put("w/t/1", 3, from1)
	caches.deltas.put("w/t/2", 3, from2)
	if got, ok := caches.deltas.get("w/t/1", 3); !ok || got != from1 {
		t.Fatal("a watcher at version 1 got another cursor's delta")
	}
	if got, ok := caches.deltas.get("w/t/2", 3); !ok || got != from2 {
		t.Fatal("a watcher at version 2 got another cursor's delta")
	}
}
