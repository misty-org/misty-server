package api

import (
	"github.com/kannachi323/misty/server/internal/agenttools"
)

// Private and public layers (docs/design/spaces-as-profiles/BRIEF.md#private-and-public)
// are the only permission system. Every agent tool declares the layer it
// touches (agenttools.Layer). Private tools reach a person's own trees:
// memory, devices (folders, tabs, bookmarks, screens), connected apps,
// private chats and agent settings.

// layerRun is who a run works for.
type layerRun struct {
	// askerUserID asked for the run and pays for it.
	askerUserID string
	// agentOwnerUserID owns the agent that runs. For Misty itself, and for an
	// agent run on its owner's own ask, it is the asker.
	agentOwnerUserID string
}

// privateLayerAllowed is the one check: a private layer opens only when the
// run's asker, the agent's owner and the layer's owner are the same person.
// A teammate's @mention of your agent is their run and sees public items
// only; so does anyone else's agent.
func privateLayerAllowed(run layerRun, layerOwnerUserID string) bool {
	return run.askerUserID != "" && run.askerUserID == run.agentOwnerUserID && run.agentOwnerUserID == layerOwnerUserID
}

// ownRun is a run its owner asked of their own agent (or of Misty).
func ownRun(userID string) layerRun {
	return layerRun{askerUserID: userID, agentOwnerUserID: userID}
}

// layeredRegistrations keeps a run's private tools only when the private
// layer opens for it. The tools act as the agent's owner, so the layer they
// reach is the owner's. Private tools are never registered otherwise, so the
// model never sees them.
func layeredRegistrations(run layerRun, registrations []agenttools.Registration) []agenttools.Registration {
	if privateLayerAllowed(run, run.agentOwnerUserID) {
		return registrations
	}
	return agenttools.PublicOnly(registrations)
}

// TestingPrivateLayerAllowed exposes the check to the table-driven test.
func TestingPrivateLayerAllowed(askerUserID, agentOwnerUserID, layerOwnerUserID string) bool {
	return privateLayerAllowed(layerRun{askerUserID: askerUserID, agentOwnerUserID: agentOwnerUserID}, layerOwnerUserID)
}
