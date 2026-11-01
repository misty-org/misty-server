package api

import (
	"context"
	"errors"
	"strconv"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/integrations/composio"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// usageLimitToolError turns a used-up monthly allowance into a tool result the
// agent can relay. It starts with the code (app_action_limit_reached), like the
// other coded tool messages, so the app can recognize it. Other errors pass
// through unchanged.
func usageLimitToolError(err error) error {
	var limit db.UsageLimitError
	if !errors.As(err, &limit) {
		return err
	}
	renews := "at the start of the next billing period"
	if !limit.Renews.IsZero() {
		renews = "on " + limit.Renews.UTC().Format("January 2") + " (UTC)"
	}
	message := limit.Code() + ": This account used its " + strconv.FormatInt(limit.Limit, 10) +
		" app actions for this billing period. Tell the user, and continue without it; the allowance renews " + renews + "."
	return errors.Join(db.ErrAgentToolboxNotAttempted, agent.ErrInvalidRequest(message), err)
}

// consumeAppAction counts one integration.execute against the account's
// month, or refuses it once the month's app actions are used.
func (s *SpacesService) consumeAppAction(ctx context.Context, userID string) error {
	return usageLimitToolError(s.database.ConsumeAccountUsage(ctx, userID, db.UsageAppActions, 1))
}

func (s *SpacesService) releaseAppAction(ctx context.Context, userID string) {
	_ = s.database.ReleaseAccountUsage(context.WithoutCancel(ctx), userID, db.UsageAppActions, 1)
}

// releaseRejectedAppAction gives the action back when Composio refused the
// call before running it; a call the app ran and failed still counts.
func (s *SpacesService) releaseRejectedAppAction(ctx context.Context, userID string, err error) {
	if err != nil && composio.Rejected(err) {
		s.releaseAppAction(ctx, userID)
	}
}
