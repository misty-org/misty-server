package api

import (
	"context"
	"errors"
	"strings"

	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// SDK permissions and Space-role permissions are different vocabularies.
func appScopeForTool(descriptor agenttools.Descriptor) string {
	switch descriptor.Name {
	case "context.get", "weather.current":
		return "ai.read"
	case "browser.click", "browser.type", "browser.interact":
		return "browser.interact"
	case "browser.downloads.list", "browser.request_user_action":
		return "browser.inspect"
	case "browser.inspect", "browser.navigate":
		return descriptor.Name
	}
	domain, _, _ := strings.Cut(descriptor.Name, ".")
	switch domain {
	case "notes", "drawings", "tasks", "calendar", "roadmaps", "messages", "library", "files", "agents", "mail":
		if descriptor.Risk == "read" {
			return domain + ".read"
		}
		return domain + ".write"
	case "members":
		return "spaces.read"
	case "browser":
		return descriptor.RequiredPermission
	}
	// Unknown or third-party effects require their explicit descriptor scope.
	return descriptor.RequiredPermission
}

func authorizeAppRuntimeTool(ctx context.Context, database *db.Database, invocation agenttools.Invocation, descriptor agenttools.Descriptor) (bool, error) {
	if database == nil {
		return true, nil
	} // Pure registry construction has no admitted run.
	authority, err := database.ExecutionAuthorityForRun(ctx, invocation.RunID, invocation.UserID)
	if err != nil {
		return false, err
	}
	if binding := descriptor.ProviderBinding; binding != nil {
		if authority != nil {
			ctx = db.WithAppExecutionAuthority(ctx, db.AppRuntimeSession{AuthorityGeneration: authority.Generation, UserID: authority.UserID, AppID: authority.AppID, SpaceID: authority.SpaceID, Scopes: authority.Scopes})
		}
		bound, resolveErr := database.ResolveSDKBoundCapability(ctx, invocation.UserID, binding.TargetID, binding.TargetRevision, binding.Capability, binding.CapabilityVersion)
		if resolveErr != nil || bound.Provider.ID != binding.ProviderID || bound.Provider.Version != binding.ProviderVersion {
			return false, resolveErr
		}
		if authority == nil {
			return true, nil
		}
		err = database.ValidateAppExecutionAuthority(ctx, authority, invocation.UserID, bound.Target.SpaceID, binding.RequiredScopes...)
		if errors.Is(err, db.ErrAppRuntimeForbidden) {
			return false, nil
		}
		return err == nil, err
	}
	if authority == nil {
		return true, nil
	}
	err = database.ValidateAppExecutionAuthority(ctx, authority, invocation.UserID, invocation.SpaceID, appScopeForTool(descriptor))
	if errors.Is(err, db.ErrAppRuntimeForbidden) {
		return false, nil
	}
	return err == nil, err
}

func authorizeAppContextReference(ctx context.Context, database *db.Database, userID string, reference aiContextReference) error {
	authority := db.AppAuthorityFromContext(ctx)
	if authority == nil {
		return nil
	}
	scopes := map[string][]string{
		"note": {"notes.read"}, "notes": {"notes.read"}, "drawing": {"drawings.read"},
		"task": {"tasks.read"}, "planner.task": {"tasks.read"}, "planner.query": {"tasks.read"},
		"agenda.range": {"tasks.read", "calendar.read"}, "space.chat": {"messages.read"},
		"roadmap": {"roadmaps.read"}, "planner.roadmap": {"roadmaps.read"},
		"library.item": {"library.read"}, "mail.thread": {"mail.read"},
		"agent.artifact": {"ai.read"}, "route": {"navigation.write"}, "space": {"spaces.read"},
	}[strings.ToLower(strings.TrimSpace(reference.Kind))]
	if len(scopes) == 0 {
		return db.ErrAppRuntimeForbidden
	}
	// Space-bound apps must identify their Space before any resource is loaded.
	if authority.SpaceID != "" && reference.SpaceID != authority.SpaceID {
		return db.ErrAppRuntimeForbidden
	}
	return database.ValidateAppExecutionAuthority(ctx, authority, userID, reference.SpaceID, scopes...)
}
