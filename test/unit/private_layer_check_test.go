package unit

import (
	"testing"

	"github.com/kannachi323/misty/server/internal/agenttools"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

// The one check: a private layer opens only when the run's asker, the agent's
// owner and the layer's owner are the same person.
func TestPrivateLayerAllowed(t *testing.T) {
	cases := []struct {
		name                     string
		asker, agentOwner, layer string
		want                     bool
	}{
		{"you ask your agent about your layer", "you", "you", "you", true},
		{"a teammate asks your agent", "mate", "you", "you", false},
		{"you ask a teammate's agent", "you", "mate", "you", false},
		{"a teammate's agent reaching your layer", "mate", "mate", "you", false},
		{"your agent reaching a teammate's layer", "you", "you", "mate", false},
		{"no asker", "", "", "", false},
		{"no asker for your agent", "", "you", "you", false},
	}
	for _, c := range cases {
		if got := api.TestingPrivateLayerAllowed(c.asker, c.agentOwner, c.layer); got != c.want {
			t.Errorf("%s: privateLayerAllowed = %v, want %v", c.name, got, c.want)
		}
	}
}

// A teammate's @mention of your agent never sees your private tools: they are
// not registered, so the model doesn't see them.
func TestPrivateToolsAreAbsentFromATeammatesRun(t *testing.T) {
	for _, name := range api.TestingMemberRequestToolNames("mate", "you") {
		if len(name) > 7 && name[:7] == "memory." {
			t.Errorf("a teammate's ask offers %s", name)
		}
	}
	owner := map[string]bool{}
	for _, name := range api.TestingMemberRequestToolNames("you", "you") {
		owner[name] = true
	}
	if !owner["memory.pref.list"] {
		t.Error("your own ask in a Space chat lost your agent's memory")
	}

	teammate := api.TestingRunToolLayers("mate", "you")
	for name, layer := range teammate {
		if layer == agenttools.LayerPrivate {
			t.Errorf("a teammate's run offers private tool %s", name)
		}
	}
	private := 0
	for _, layer := range api.TestingRunToolLayers("you", "you") {
		if layer == agenttools.LayerPrivate {
			private++
		}
	}
	if private == 0 {
		t.Error("your own run offers no private tools")
	}
}
