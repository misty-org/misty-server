package api

import (
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// spaceToolRegistrations are Space-bound tools with their own handlers, beside
// the older Space data tools: threads and note tags.
func spaceToolRegistrations(database *db.Database) []agenttools.Registration {
	return append(threadToolRegistrations(database), noteTagsToolRegistration(database))
}
