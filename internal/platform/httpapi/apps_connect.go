package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/integrations/composio"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// appsConnect shows a Connect card and waits briefly for the user to sign in.
// If the user has not connected by then, the run hands off: the card stays in
// the chat and Misty continues the conversation once the app is connected.
func (s *SpacesService) appsConnect(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		App string `json:"app"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil {
		return nil, agenttools.ErrArgumentsInvalid
	}
	app := strings.ToLower(strings.TrimSpace(input.App))
	if !composio.ValidToolkit(app) {
		return nil, serveragent.ErrInvalidRequest("app must be an app slug from apps_search, such as googledrive")
	}
	client, err := composioClient()
	if err != nil {
		return nil, appsError(err)
	}
	connected := func() (bool, error) { return appsAccountActive(ctx, client, invocation.UserID, app) }
	if ok, err := connected(); err != nil || ok {
		return appsConnectResult(app, ok, err)
	}
	name := s.appsName(ctx, invocation.UserID, app)
	pending, err := s.database.OpenAgentAppRequest(ctx, db.AgentAppRequest{
		OwnerUserID: invocation.UserID, RunID: invocation.RunID, Kind: "connect", Subject: app,
		Title: "Connect " + name, Summary: "Sign in to " + name + " so Misty can continue.",
	}, appsRequestTTL)
	if err != nil {
		return nil, err
	}
	if pending.State == "declined" {
		return nil, serveragent.ErrInvalidRequest("The user dismissed connecting " + name + ". Continue without it, or ask what they would prefer.")
	}
	s.showAppRequest(ctx, invocation.UserID, invocation.RunID, pending)
	ready := false
	_, err = appsWaitFor(ctx, appsWait, 2*time.Second, func() (bool, error) {
		if ok, err := connected(); err != nil || ok {
			ready = ok
			return ok, err
		}
		current, err := s.database.AgentAppRequest(ctx, invocation.UserID, pending.ID)
		if err == nil {
			pending = current
		}
		return pending.State != "pending", err
	})
	if err != nil {
		return nil, appsError(err)
	}
	if ready {
		if resolved, _ := s.database.ResolveAgentAppRequest(ctx, invocation.UserID, pending.ID, "pending", "connected"); resolved {
			pending.State = "connected"
			s.showAppRequest(ctx, invocation.UserID, invocation.RunID, pending)
		}
		return appsConnectResult(app, true, nil)
	}
	if pending.State == "declined" {
		return nil, serveragent.ErrInvalidRequest("The user dismissed connecting " + name + ". Continue without it, or ask what they would prefer.")
	}
	return TestingMustAPIRawJSON(map[string]any{
		"app": app, "status": "waiting_for_user",
		"message":      "A Connect " + name + " card is showing in the chat. This run ends here and Misty continues once the user connects " + name + ".",
		"user_message": "Connect " + name + " with the card above. Misty continues once it's connected.",
	}), nil
}

func appsAccountActive(ctx context.Context, client *composio.Client, userID, app string) (bool, error) {
	accounts, err := client.Accounts(ctx, composio.UserID(userID), app)
	for _, account := range accounts {
		if account.Active() {
			return true, nil
		}
	}
	return false, err
}

// refreshConnectRequest marks a pending Connect card connected once the app
// is, so a card left after the run ended can continue the conversation.
func (s *SpacesService) refreshConnectRequest(ctx context.Context, user string, request *db.AgentAppRequest) *db.AgentAppRequest {
	if request.Kind != "connect" || request.State != "pending" || !time.Now().Before(request.ExpiresAt) {
		return request
	}
	client, err := composioClient()
	if err != nil {
		return request
	}
	if ok, err := appsAccountActive(ctx, client, user, request.Subject); err != nil || !ok {
		return request
	}
	if resolved, _ := s.database.ResolveAgentAppRequest(ctx, user, request.ID, "pending", "connected"); resolved {
		request.State = "connected"
		s.showAppRequest(ctx, user, request.RunID, request)
	}
	return request
}

func appsConnectResult(app string, ok bool, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, appsError(err)
	}
	return TestingMustAPIRawJSON(map[string]any{"app": app, "status": "connected", "connected": ok}), nil
}

// appsName is the app's display name, or its slug when Composio has none.
func (s *SpacesService) appsName(ctx context.Context, user, app string) string {
	name := app
	_ = s.withAppsSession(ctx, user, func(client *composio.Client, session string) error {
		items, _, err := client.Toolkits(ctx, session, app, "", false)
		for _, item := range items {
			if item.Slug == app && strings.TrimSpace(item.Name) != "" {
				name = strings.TrimSpace(item.Name)
			}
		}
		return err
	})
	return name
}
