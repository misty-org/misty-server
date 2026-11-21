package api

import "testing"

// Presence names a place, and an item only where that place has one: a notebook's id in the Lab,
// an app's id, or one of the Studio's rooms by its fixed name. Anything else is dropped, so it never carries
// free text.
func TestCleanPresencePlaceKeepsOnlyKnownItems(t *testing.T) {
	cases := []struct{ place, item, wantPlace, wantItem string }{
		{"lab", "note_0123-abc", "lab", "note_0123-abc"},
		{"lab", "not an id / ../x", "lab", ""},
		{"studio", "lounge", "studio", "lounge"},
		{"studio", "desk", "studio", "desk"},
		{"studio", "note_0123-abc", "studio", ""},
		{"studio", "my secret plans", "studio", ""},
		{"app", "deckcraft", "app", "deckcraft"},
		{"app", "My Secret App", "app", ""},
		{"app", "note_0123-abc", "app", ""},
		{"my secret plans", "lounge", "", ""},
		{"", "lounge", "", ""},
	}
	for _, c := range cases {
		place, item := cleanPresencePlace(c.place, c.item)
		if place != c.wantPlace || item != c.wantItem {
			t.Errorf("cleanPresencePlace(%q, %q) = (%q, %q), want (%q, %q)",
				c.place, c.item, place, item, c.wantPlace, c.wantItem)
		}
	}
}
