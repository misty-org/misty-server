package api

import (
	"encoding/json"
)

// defaultSpaceContextSections is what a session with no personal agent reads.
// A personal agent narrows this to its owner-configured ContextPermissions.
//
// "task_notes" is the free-text notes column on a task, not the Notes surface.
// The Notes surface is device-local, so the server cannot read it at all; the
// capability card tells the agent to ask the member to paste instead.
var defaultSpaceContextSections = json.RawMessage(
	`{"space_chat":true,"library":true,"task_notes":true,"tasks":true,"members":true}`,
)
