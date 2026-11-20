package db

import "testing"

func TestSpacePersonalItemKeys(t *testing.T) {
	for _, key := range []string{"chat:everyone", "note:a_b", "file:f"} {
		if !ValidSpaceItemKey(key) {
			t.Errorf("rejected %q", key)
		}
	}
	for _, key := range []string{"home:all", "chat:", "file:../secret", "note:x/y", "task:a?b", "file:with space", "decks:x", "sheet:x", "task:t_1", "drawing:x", "deck:deck_1", "workbook:workbook_1"} {
		if ValidSpaceItemKey(key) {
			t.Errorf("accepted %q", key)
		}
	}
}
