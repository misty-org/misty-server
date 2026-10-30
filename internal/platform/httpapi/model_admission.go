package api

import (
	"context"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/byok"
	"github.com/kannachi323/misty/server/internal/modelruntime"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// Model admission. A role runs on, in order: the model the account chose for
// it on its device with that provider's key (X-Misty-Models, see byok), the
// operator's models (MISTY_AGENT_MODEL, or a development eval stack), or
// nothing: byok.ErrModelKeyNeeded, answered 409 model_key_needed.

// accountCredentials is what the account brought, when it can run anything.
func accountCredentials(ctx context.Context, user string) *byok.Credentials {
	if c := byok.Default.For(ctx, user); c.Usable() {
		return c
	}
	return nil
}

// reasoningRole reports whether a role takes a reasoning effort.
func reasoningRole(role string) bool {
	for _, r := range aimodels.Roles {
		if r.ID == role {
			return r.Reasoning
		}
	}
	return false
}

// accountReasoning is the account's effort, "low" unless it chose one.
func accountReasoning(c *byok.Credentials) string {
	if c != nil && c.Reasoning != "" {
		return c.Reasoning
	}
	return agent.HouseReasoning
}

// shortReasoning keeps OpenAI reasoning models brief on calls that need no
// deliberation; other models take their default.
func shortReasoning(model string) string {
	if strings.HasPrefix(model, "openai/") {
		return "low"
	}
	return ""
}

// resolveRoleModel is the account's model for a role. nil means the
// operator's default runs it.
func resolveRoleModel(ctx context.Context, database *db.Database, user, role string) (*aimodels.Resolved, error) {
	if c := accountCredentials(ctx, user); c != nil {
		model := c.RoleModel(role)
		if model == "" {
			// Embeddings are off without an embedding model; memory and
			// Library search fall back to text matching.
			if role == "embedding" {
				return nil, aimodels.ErrDisabled
			}
			if !aimodels.OperatorModels() {
				return nil, byok.ErrModelKeyNeeded
			}
			return nil, nil
		}
		credential, ok := c.CredentialFor(model)
		if !ok {
			return nil, byok.ErrModelKeyNeeded
		}
		reasoning := ""
		if reasoningRole(role) {
			reasoning = accountReasoning(c)
		}
		return &aimodels.Resolved{Model: model, Reasoning: reasoning, Credential: &credential}, nil
	}
	if !aimodels.OperatorModels() {
		return nil, byok.ErrModelKeyNeeded
	}
	if database == nil {
		return nil, nil
	}
	routes, err := database.AIModelRoutes(ctx, user)
	if err != nil {
		return nil, err
	}
	r := modelRoute(routes, role)
	if !r.Enabled {
		return nil, aimodels.ErrDisabled
	}
	if r.Model == "" {
		return nil, nil
	}
	return &aimodels.Resolved{Model: r.Model, Reasoning: r.Reasoning}, nil
}

// roleRoute is how one of the server's own calls for role runs for user.
func roleRoute(ctx context.Context, database *db.Database, user, role string) (modelruntime.Route, string, error) {
	resolved, err := resolveRoleModel(ctx, database, user, role)
	if err != nil {
		return modelruntime.Route{}, "", err
	}
	if resolved != nil {
		return modelruntime.For(resolved), resolved.Model, nil
	}
	return modelruntime.Instance(), defaultRoleModel(role), nil
}

// accountRunRoutes puts the account's own role models into a run's planned
// routes. The thinking and seeing roles keep the model admission chose (it is
// the account's own, or an eval's pick).
func accountRunRoutes(ctx context.Context, user string, routes []aimodels.Route) []aimodels.Route {
	c := accountCredentials(ctx, user)
	if c == nil {
		return routes
	}
	out := make([]aimodels.Route, len(routes))
	for i, r := range routes {
		out[i] = r
		switch r.Role {
		case "agent", "vision", "browsing":
			continue
		}
		if model := c.RoleModel(r.Role); model != "" {
			out[i].Model = model
			if reasoningRole(r.Role) {
				out[i].Reasoning = accountReasoning(c)
			}
		}
	}
	return out
}
