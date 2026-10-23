package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// The memory tree (docs/plans/spaces-as-profiles.md, phase 7) is one per
// person per Space, on the server. Requests name the Space of the window they
// came from; runs carry it to their tools. Nothing waits for a device.

// requestMemorySpace resolves the Space a memory request names (the
// space_id query parameter or body field), or the personal Space when it
// names none. A Space the person doesn't belong to is refused.
func requestMemorySpace(ctx context.Context, database *db.Database, user, requested string) (string, error) {
	return database.MemorySpace(ctx, user, requested)
}

// runMemorySpace is the Space whose tree a run reads and writes: the Space
// of the window it was asked from, else the Space it runs in, else the
// asker's personal Space.
func runMemorySpace(ctx context.Context, database *db.Database, invocation agenttools.Invocation) (string, error) {
	requested := invocation.MemorySpaceID
	if requested == "" {
		requested = invocation.SpaceID
	}
	return database.MemorySpace(ctx, invocation.UserID, requested)
}

func actorMemorySpace(ctx context.Context, database *db.Database, actor spaceConversationToolActor) (string, error) {
	return runMemorySpace(ctx, database, agenttools.Invocation{UserID: actor.userID, SpaceID: actor.spaceID, MemorySpaceID: actor.memorySpaceID})
}

// memorySpaceParam is the window's Space on a memory request.
func memorySpaceParam(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("space_id"))
}
