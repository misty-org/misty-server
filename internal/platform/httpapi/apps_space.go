package api

import (
	"context"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/integrations/composio"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// A run uses the person's own connected accounts, never anyone else's.
// Composio keeps one session per person, so Misty names the account on every
// call and refuses an account that isn't the person's.

// runApps is what one run may use: every active account the person has, by app.
type runApps struct {
	active map[string][]composio.Account
}

// appsNeed is why a call can't run yet.
type appsNeed string

const (
	appsReady appsNeed = ""
	// appsNeedConnect: the person has no account for the app at all.
	appsNeedConnect appsNeed = "connect"
	// appsNeedChoose: the person has several and the call named none.
	appsNeedChoose appsNeed = "choose"
	// appsNeedOther: the call named an account that isn't theirs.
	appsNeedOther appsNeed = "other"
)

func newRunApps(accounts []composio.Account) runApps {
	apps := runApps{active: map[string][]composio.Account{}}
	for _, account := range accounts {
		if account.Active() {
			apps.active[account.Toolkit.Slug] = append(apps.active[account.Toolkit.Slug], account)
		}
	}
	return apps
}

// account resolves the account one call runs as. requested may be an account
// ID or alias; empty means the person's only account for the app.
func (a runApps) account(toolkit, requested string) (string, appsNeed) {
	accounts := a.active[toolkit]
	if len(accounts) == 0 {
		return "", appsNeedConnect
	}
	if requested == "" {
		if len(accounts) == 1 {
			return accounts[0].ID, appsReady
		}
		return "", appsNeedChoose
	}
	for _, account := range accounts {
		if account.ID == requested || (account.Alias != "" && account.Alias == requested) {
			return account.ID, appsReady
		}
	}
	return "", appsNeedOther
}

// choices names the person's accounts for a model-facing message.
func (a runApps) choices(toolkit string) string {
	names := []string{}
	for _, account := range a.active[toolkit] {
		name := account.ID
		if account.Alias != "" {
			name += " (" + account.Alias + ")"
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// loadRunApps reads the person's accounts. Apps are the private layer: a run
// it doesn't open for gets none, even if an app tool reached it some other way.
func (s *SpacesService) loadRunApps(ctx context.Context, client *composio.Client, invocation agenttools.Invocation) (runApps, error) {
	if invocation.PublicLayerOnly || db.PublicLayerOnly(ctx) {
		return runApps{}, agenttools.ErrCapabilityDenied
	}
	accounts, err := client.Accounts(ctx, composio.UserID(invocation.UserID), "")
	if err != nil {
		return runApps{}, err
	}
	return newRunApps(accounts), nil
}

// appsNeedError tells the model what to do when a call can't run yet.
func appsNeedError(apps runApps, toolkit string, need appsNeed) error {
	switch need {
	case appsNeedConnect:
		return agent.ErrInvalidRequest("The user has not connected " + toolkit + ". Call integration_connect with app " + toolkit + " first.")
	case appsNeedChoose:
		return agent.ErrInvalidRequest("The user has several " + toolkit + " accounts. Pass account as one of: " + apps.choices(toolkit) + ".")
	case appsNeedOther:
		return agent.ErrInvalidRequest("That " + toolkit + " account isn't one of the user's. Pass account as one of: " + apps.choices(toolkit) + ", or leave it out.")
	}
	return nil
}
