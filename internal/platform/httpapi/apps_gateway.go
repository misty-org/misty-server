package api

import (
	"context"
	"errors"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/integrations/composio"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Connected apps run through Composio. Each account has one session that can
// reach every toolkit. Misty decides when an action needs the user, shows the
// card in chat, and journals every write.
const (
	appsSearchTool    = "apps.search"
	appsSchemasTool   = "apps.schemas"
	appsConnectedTool = "apps.connected"
	appsConnectTool   = "apps.connect"
	appsExecuteTool   = "apps.execute"
	// One call waits this long for the user; the runtime's MCP request limit is 60s.
	appsWait       = 40 * time.Second
	appsRequestTTL = 15 * time.Minute
	// The model sees at most this much of one app result.
	appsResultLimit = 48 << 10
)

// appRequest is the chat card for something an agent needs from the user.
type appRequest struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Subject   string    `json:"subject"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func appRequestView(request *db.AgentAppRequest) *appRequest {
	return &appRequest{ID: request.ID, Kind: request.Kind, Subject: request.Subject, Title: request.Title, Summary: request.Summary, State: request.State, ExpiresAt: request.ExpiresAt}
}

func appsAvailable() bool { return composio.ConfigFromEnv().Validate() == nil }

func composioClient() (*composio.Client, error) { return composio.New(composio.ConfigFromEnv()) }

// appsSession returns the account's session, creating it on first use or
// when Composio no longer has the saved one.
func (s *SpacesService) appsSession(ctx context.Context, client *composio.Client, user string, renew bool) (string, error) {
	if !renew {
		session, err := s.database.ComposioSession(ctx, user)
		if err == nil {
			return session, nil
		}
		if !errors.Is(err, db.ErrSpaceNotFound) {
			return "", err
		}
	}
	session, err := client.CreateSession(ctx, composio.UserID(user))
	if err != nil {
		return "", err
	}
	return session, s.database.SaveComposioSession(ctx, user, session)
}

// withAppsSession runs one session call. A 404 means nothing ran, so the call
// is safe to repeat once on a fresh session.
func (s *SpacesService) withAppsSession(ctx context.Context, user string, call func(*composio.Client, string) error) error {
	client, err := composioClient()
	if err != nil {
		return err
	}
	session, err := s.appsSession(ctx, client, user, false)
	if err != nil {
		return err
	}
	if err = call(client, session); !composio.NotFound(err) {
		return err
	}
	if session, err = s.appsSession(ctx, client, user, true); err != nil {
		return err
	}
	return call(client, session)
}

// appsError turns a Composio rejection into a message the model can act on.
// Anything else keeps its unknown outcome.
func appsError(err error) error {
	if errors.Is(err, composio.ErrUnavailable) {
		return serveragent.ErrInvalidRequest("Connected apps are not set up on this Misty server.")
	}
	var apiErr *composio.APIError
	if composio.Rejected(err) && errors.As(err, &apiErr) {
		return serveragent.ErrInvalidRequest(apiErr.Message)
	}
	return err
}

// appsWaitFor polls check until it reports done or the wait ends.
func appsWaitFor(ctx context.Context, wait, every time.Duration, check func() (bool, error)) (bool, error) {
	deadline := time.Now().Add(wait)
	for {
		done, err := check()
		if err != nil || done {
			return done, err
		}
		if time.Now().Add(every).After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(every):
		}
	}
}

// showAppRequest puts a request's card in the run's chat, when it has one.
func (s *SpacesService) showAppRequest(ctx context.Context, user, runID string, request *db.AgentAppRequest) {
	if s.aiInvocations == nil || request == nil {
		return
	}
	invocationID := ""
	if isAIInvocationRuntimeID(runID) {
		record, err := s.database.AIInvocationByID(ctx, user, runID)
		if err != nil {
			return
		}
		if _, err := s.aiInvocations.restoreDurable(ctx, *record); err != nil {
			return
		}
		invocationID = record.ID
	} else if strings.HasPrefix(runID, "run_") {
		run, err := s.database.SpaceRun(ctx, user, runID)
		if err != nil {
			return
		}
		_, invocationID = s.restoreLinkedAIInvocation(ctx, run)
	}
	if invocationID != "" {
		_ = s.aiInvocations.append(invocationID, aiInvocationEvent{Type: "app.request", AppRequest: appRequestView(request)})
	}
}
