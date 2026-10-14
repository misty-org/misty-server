package agent

import "testing"

func TestCleanConversationTitle(t *testing.T) {
	cases := map[string]string{
		`"Vegetarian weight loss plan."`:                   "Vegetarian weight loss plan",
		"Title: fix login redirect loop\nBecause it loops": "Fix login redirect loop",
		"NONE":     "",
		"  none. ": "",
		"A very long title that keeps going well past the limit": "A very long title that keeps going well past",
	}
	for input, want := range cases {
		if got := CleanConversationTitle(input); got != want {
			t.Errorf("CleanConversationTitle(%q) = %q, want %q", input, got, want)
		}
	}
}
