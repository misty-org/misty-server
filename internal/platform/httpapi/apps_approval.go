package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/integrations/composio"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// appsApproval applies the run's Ask or Auto to one exact app action. Sends,
// shares, deletes and payments need the person unless, in Auto, they
// remembered an approval for this connected account (or, for a payment, this
// merchant up to a cap). Smaller writes pause only in Ask. It returns the
// approval to consume, or a result that tells the model the card is waiting.
func (s *SpacesService) appsApproval(ctx context.Context, invocation agenttools.Invocation, info composio.ToolInfo, call appsCall, kind composio.Kind) (*db.AgentAppRequest, json.RawMessage, error) {
	title, summary := appsActionSummary(info, call)
	return s.gateAction(ctx, invocation, appsGrantNeed(info, call, kind), info.Slug, appsArgumentsHash(call), title, summary)
}

// appsGrantNeed is what an app action needs: payments name the app as the
// merchant and carry the amount when the arguments state one; other
// consequential actions name the one connected account they act through.
func appsGrantNeed(info composio.ToolInfo, call appsCall, kind composio.Kind) grantNeed {
	if !kind.NeedsApproval() && !appsPaymentAction(info.Slug) {
		return grantNeed{action: agent.ActionToolWrite}
	}
	app := strings.TrimSpace(info.Toolkit.Name)
	if app == "" {
		app = info.Toolkit.Slug
	}
	toolkit := strings.ToLower(strings.TrimSpace(info.Toolkit.Slug))
	if appsPaymentAction(info.Slug) {
		need := grantNeed{action: agent.GrantPayment, scopeKind: agent.ScopeMerchant, scopeID: toolkit, amount: appsPaymentAmount(call)}
		if need.amount != nil {
			need.label = fmt.Sprintf("pay through %s up to %s", app, formatGrantAmount(*need.amount))
		}
		return need
	}
	account := strings.TrimSpace(call.Account)
	label := "act in " + app
	if account == "" {
		account = "default"
	} else {
		label += " (" + account + ")"
	}
	return grantNeed{action: agent.GrantAppAction, scopeKind: agent.ScopeConnectedAccount, scopeID: toolkit + ":" + account, label: label}
}

// appsPaymentAmount reads an amount in minor units and its currency when the
// action's arguments carry them (Stripe and Square style); otherwise the
// payment cannot be remembered, since a grant needs a cap.
func appsPaymentAmount(call appsCall) *grantAmount {
	var fields struct {
		Amount   json.Number `json:"amount"`
		Currency string      `json:"currency"`
	}
	if json.Unmarshal(call.Arguments, &fields) != nil {
		return nil
	}
	amount, err := fields.Amount.Int64()
	currency := strings.ToLower(strings.TrimSpace(fields.Currency))
	if err != nil || amount <= 0 || len(currency) != 3 {
		return nil
	}
	return &grantAmount{AmountMinor: amount, Currency: currency}
}

func formatGrantAmount(amount grantAmount) string {
	return fmt.Sprintf("%d.%02d %s", amount.AmountMinor/100, amount.AmountMinor%100, strings.ToUpper(amount.Currency))
}

// appsPaymentWords mark an app action that moves money, by the words of its
// slug (STRIPE_CREATE_PAYMENT_INTENT, SQUARE_CREATE_ORDER_CHECKOUT).
var appsPaymentWords = map[string]bool{
	"PAY": true, "PAYMENT": true, "PAYMENTS": true, "CHARGE": true, "PURCHASE": true, "BUY": true,
	"CHECKOUT": true, "TRANSFER": true, "PAYOUT": true, "REFUND": true, "CAPTURE": true,
}

func appsPaymentAction(slug string) bool {
	for _, word := range strings.Split(strings.ToUpper(slug), "_") {
		if appsPaymentWords[word] {
			return true
		}
	}
	return false
}

// appsActionSummary is the approval card: the app and action, then the exact
// arguments the user is approving.
func appsActionSummary(info composio.ToolInfo, call appsCall) (string, string) {
	app := strings.TrimSpace(info.Toolkit.Name)
	if app == "" {
		app = info.Toolkit.Slug
	}
	action := strings.TrimSpace(info.Name)
	if action == "" {
		action = strings.ToLower(strings.ReplaceAll(info.Slug, "_", " "))
	}
	title := truncateAgentRuntimeText(app+" · "+action, 200)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(call.Arguments, &fields)
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := []string{}
	for index, key := range keys {
		if index == 10 {
			lines = append(lines, fmt.Sprintf("…and %d more fields", len(keys)-index))
			break
		}
		var text string
		if json.Unmarshal(fields[key], &text) != nil {
			text = string(fields[key])
		}
		lines = append(lines, key+": "+truncateAgentRuntimeText(strings.TrimSpace(text), 400))
	}
	if call.Account != "" {
		lines = append(lines, "account: "+call.Account)
	}
	return title, truncateAgentRuntimeText(strings.Join(lines, "\n"), 3800)
}
