package db

import (
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)


import (
	"encoding/json"
)


func mustTestRaw(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }

func containsSpaceRun(items []SpaceRun, runID string) bool {
	for _, item := range items {
		if item.ID == runID {
			return true
		}
	}
	return false
}
