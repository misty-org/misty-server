package db

import "testing"

func TestSpacePersonalItemKeys(t *testing.T) {
	for _, key := range []string{"chat:everyone", "task:uuid-123", "note:a_b", "drawing:x", "file:f"} {
		if !ValidSpaceItemKey(key) {
			t.Errorf("rejected %q", key)
		}
	}
	for _, key := range []string{"home:all", "chat:", "file:../secret", "note:x/y", "task:a?b", "file:with space"} {
		if ValidSpaceItemKey(key) {
			t.Errorf("accepted %q", key)
		}
	}
}
