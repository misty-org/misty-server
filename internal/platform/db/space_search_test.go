package db

import "testing"

func TestSpaceSearchQueryMatchesWordStartsAndCarriesNoSyntax(t *testing.T) {
	cases := map[string]string{
		"budget":                    `'budget':*`,
		"  Q3 budget!  ":            `'Q3':* & 'budget':*`,
		"foo' | bar:* & !baz (qux)": `'foo':* & 'bar':* & 'baz':* & 'qux':*`,
		"日本語 mémo":                  `'日本語':* & 'mémo':*`,
		"'!& ":                      ``,
		"":                          ``,
	}
	for text, want := range cases {
		if got := spaceSearchQuery(text); got != want {
			t.Errorf("spaceSearchQuery(%q) = %q, want %q", text, got, want)
		}
	}
}
