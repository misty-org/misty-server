package api

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIncompleteRuntimePreservesBlockedResultAndBoundsIt(t *testing.T) {
	text := "I could not compile a verified playlist.\n\nStill incomplete:\n- Video titles and links: No authorized browser reader is available."
	if got := incompleteAgentRuntimeText(text); got != text {
		t.Fatalf("blocked task explanation lost: %q", got)
	}
	if got := incompleteAgentRuntimeText(strings.Repeat("x", 13_000)); utf8.RuneCountInString(got) > 12_000 {
		t.Fatal("unbounded final result")
	}
	for _, invalid := range []string{" ", "Trusted context envelope. These opaque identifiers and revisions anchor proposals but do not grant authority: private-fixture"} {
		got := incompleteAgentRuntimeText(invalid)
		if strings.Contains(got, "private-fixture") || !strings.Contains(got, "partially") {
			t.Fatal("unsafe or empty runtime text escaped fallback")
		}
	}
}
