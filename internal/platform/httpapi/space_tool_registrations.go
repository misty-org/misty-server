package api

import (
	"github.com/misty-org/misty-server/internal/agenttools"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// spaceToolRegistrations are Space-bound tools with their own handlers, beside
// the older Space data tools: threads and note tags.
func spaceToolRegistrations(database *db.Database) []agenttools.Registration {
	return append(threadToolRegistrations(database), noteTagsToolRegistration(database))
}
