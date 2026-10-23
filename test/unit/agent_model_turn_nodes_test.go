package unit

import (
	"testing"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

func TestCompactionAndScreenCallsDoNotSpendModelTurns(t *testing.T) {
	for node, free := range map[string]bool{
		"model:1": false, "model:12": false,
		"model:screen:job:3": true, "model:compact:1": true,
		"model:compactor": false,
	} {
		if got := db.TurnFreeModelNode(node); got != free {
			t.Fatalf("TurnFreeModelNode(%q) = %v, want %v", node, got, free)
		}
	}
}
