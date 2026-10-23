package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/test/testkit"
)

// answerNextCard waits for the pending card a gate opened and answers it like
// the chat card does: continue, remember or deny.
func answerNextCard(t *testing.T, service *SpacesService, userID, subject, decision string) {
	t.Helper()
	ctx := context.Background()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		var id string
		err := service.database.SpaceTx(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, `SELECT id FROM agent_app_requests WHERE owner_user_id=$1 AND subject=$2 AND state='pending'`, userID, subject).Scan(&id)
		})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			t.Error(err)
			return
		}
		switch decision {
		case "remember":
			_, err = service.rememberAppRequest(ctx, userID, id)
		case "refuse-remember":
			if _, err := service.rememberAppRequest(ctx, userID, id); !errors.Is(err, db.ErrSpaceConflict) {
				t.Errorf("remembering %s must be refused, got %v", subject, err)
			}
			_, err = service.database.DecideAgentAppRequest(ctx, userID, id, "declined")
		case "continue":
			_, err = service.database.DecideAgentAppRequest(ctx, userID, id, "approved")
		default:
			_, err = service.database.DecideAgentAppRequest(ctx, userID, id, "declined")
		}
		if err != nil {
			t.Errorf("%s %s: %v", decision, subject, err)
		}
		return
	}
	t.Errorf("no card for %s", subject)
}

type gateOutcome struct {
	approval *db.AgentAppRequest
	waiting  json.RawMessage
	err      error
}

func TestAgentGrantsPostgres(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := t.Context()
	owner, _ := database.CreateUser("grants-owner@example.test", "password123")
	other, _ := database.CreateUser("grants-other@example.test", "password123")
	helper, err := database.SavePersonalAgent(ctx, owner.ID, "", db.AgentProfileInput{Name: "Helper", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if helper.DefaultRunMode != agent.PermissionAsk || helper.Runner != "misty" {
		t.Fatalf("a new agent starts in Ask on Misty's runner: %+v", helper)
	}
	workflow, err := database.SaveAgentMethod(ctx, owner.ID, db.AgentMethod{AgentID: helper.ID, Kind: "workflow", Enabled: true,
		Definition: db.AgentMethodDefinition{Title: "Weekly review", Instructions: "Summarize", Inputs: []db.AgentMethodInput{}, Target: "cloud", RequiredTools: []string{}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	service := &SpacesService{database: database}
	invocation := func(user, mode string) agenttools.Invocation {
		return agenttools.Invocation{UserID: user, AgentID: helper.ID, RunID: "run_grants_test", PermissionMode: mode}
	}
	gate := func(inv agenttools.Invocation, need grantNeed, subject, answer string) gateOutcome {
		t.Helper()
		done := make(chan gateOutcome, 1)
		go func() {
			approval, waiting, err := service.gateAction(ctx, inv, need, subject, subject+":hash:"+answer+time.Now().String(), "Test action", "")
			done <- gateOutcome{approval, waiting, err}
		}()
		if answer != "" {
			answerNextCard(t, service, inv.UserID, subject, answer)
		}
		out := <-done
		if out.approval != nil {
			_ = service.useAgentActionApproval(ctx, inv.UserID, out.approval)
		}
		return out
	}
	unschedule := grantNeed{action: agent.GrantScheduleRemove, scopeKind: agent.ScopeSchedule, scopeID: workflow.ID, label: "remove the schedule"}

	// Auto without a grant pauses; remembering approves and stores the grant.
	if out := gate(invocation(owner.ID, "auto"), unschedule, "s1", "remember"); out.err != nil || out.approval == nil {
		t.Fatalf("remembered approval = %+v", out)
	}
	grants, err := database.AgentGrants(ctx, owner.ID, helper.ID)
	if err != nil || len(grants) != 1 || grants[0].Action != "schedule.remove" || grants[0].ScopeID != workflow.ID {
		t.Fatalf("grants after remember = %+v, %v", grants, err)
	}
	// Auto continues on the grant without a card.
	if out := gate(invocation(owner.ID, "auto"), unschedule, "s2", ""); out.err != nil || out.approval != nil || out.waiting != nil {
		t.Fatalf("auto with a grant must continue: %+v", out)
	}
	// Ask is forceful: the grant is ignored and the card still shows.
	if out := gate(invocation(owner.ID, "ask"), unschedule, "s3", "continue"); out.err != nil || out.approval == nil {
		t.Fatalf("ask must pause despite the grant: %+v", out)
	}
	// The grantor is the resource owner: another person's run has no grant.
	if out := gate(invocation(other.ID, "auto"), unschedule, "s4", "deny"); out.err == nil {
		t.Fatalf("another grantor must pause and a deny must fail: %+v", out)
	}
	// Revoking makes Auto pause again.
	if err := database.RevokeAgentGrant(ctx, owner.ID, grants[0].ID); err != nil {
		t.Fatal(err)
	}
	if out := gate(invocation(owner.ID, "auto"), unschedule, "s5", "deny"); out.err == nil {
		t.Fatalf("revoked grant must pause: %+v", out)
	}

	// Payments are remembered per merchant with a per-payment cap.
	pay := func(amount int64) grantNeed {
		return grantNeed{action: agent.GrantPayment, scopeKind: agent.ScopeMerchant, scopeID: "stripe", label: "pay", amount: &grantAmount{AmountMinor: amount, Currency: "usd"}}
	}
	if out := gate(invocation(owner.ID, "auto"), pay(500), "p1", "remember"); out.err != nil || out.approval == nil {
		t.Fatalf("remembered payment = %+v", out)
	}
	if out := gate(invocation(owner.ID, "auto"), pay(400), "p2", ""); out.err != nil || out.approval != nil {
		t.Fatalf("payment under the cap must continue: %+v", out)
	}
	if out := gate(invocation(owner.ID, "auto"), pay(600), "p3", "deny"); out.err == nil {
		t.Fatalf("payment over the cap must pause: %+v", out)
	}
	noAmount := grantNeed{action: agent.GrantPayment, scopeKind: agent.ScopeMerchant, scopeID: "stripe"}
	if noAmount.rememberable() {
		t.Fatal("a payment without an amount cannot be remembered")
	}

	// Wildcards, unknown kinds and never-rememberable actions offer no remember.
	for _, need := range []grantNeed{
		{action: agent.GrantScheduleRemove, scopeKind: agent.ScopeSchedule, scopeID: "*"},
		{action: agent.GrantGitPush, scopeKind: agent.ScopeOrigin, scopeID: "https://github.com"},
		{action: agent.ActionScreenControl},
	} {
		if need.rememberable() {
			t.Fatalf("%+v must not be rememberable", need)
		}
	}
	// Never-rememberable actions pause even in Auto, and their card cannot be remembered.
	if out := gate(invocation(owner.ID, "auto"), grantNeed{action: agent.ActionScreenControl}, "screen", "refuse-remember"); out.err == nil {
		t.Fatalf("screen control must pause in Auto: %+v", out)
	}
	// Scope-implied changes continue in Auto and pause in Ask; reads never pause.
	if out := gate(invocation(owner.ID, "auto"), grantNeed{action: agent.ActionToolWrite}, "w1", ""); out.approval != nil || out.err != nil {
		t.Fatalf("auto write = %+v", out)
	}
	if out := gate(invocation(owner.ID, "ask"), grantNeed{action: agent.ActionToolWrite}, "w2", "continue"); out.approval == nil {
		t.Fatalf("ask write = %+v", out)
	}

	// A Space grant needs the grantor to administer that Space.
	theirs, err := database.TestingCreateSpace(ctx, other.ID, "Theirs")
	if err != nil {
		t.Fatal(err)
	}
	request := &db.AgentAppRequest{GrantAction: "space.admin", GrantScopeKind: "space", GrantScopeID: theirs.ID, GrantConstraints: json.RawMessage(`{}`)}
	if err := service.validateGrantScope(ctx, owner.ID, request); err == nil {
		t.Fatal("a grant on someone else's Space must be refused")
	}
	if revoked, err := database.RevokeAgentGrants(ctx, owner.ID, helper.ID); err != nil || revoked != 1 {
		t.Fatalf("revoke all = %d, %v", revoked, err)
	}
}

func TestAgentPermissionModeAdmissionPostgres(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := t.Context()
	owner, _ := database.CreateUser("modes-owner@example.test", "password123")
	helper, _ := database.SavePersonalAgent(ctx, owner.ID, "", db.AgentProfileInput{Name: "Helper", Enabled: true, DefaultRunMode: "auto"})
	if helper.DefaultRunMode != "auto" {
		t.Fatalf("default run mode = %q", helper.DefaultRunMode)
	}
	helper, err := database.SavePersonalAgent(ctx, owner.ID, helper.ID, db.AgentProfileInput{Name: "Helper", Enabled: true, Version: helper.Version, DefaultRunMode: "ask"})
	if err != nil || helper.DefaultRunMode != "ask" {
		t.Fatalf("editing the default = %+v, %v", helper, err)
	}
	if _, err := database.SavePersonalAgent(ctx, owner.ID, helper.ID, db.AgentProfileInput{Name: "Helper", Enabled: true, Version: helper.Version, DefaultRunMode: "full"}); err == nil {
		t.Fatal("full is no longer a mode")
	}
	body := aiInvocationInput{AgentID: helper.ID}
	if err := admitPermissionMode(ctx, database, owner.ID, &body); err != nil || body.PermissionMode != "ask" {
		t.Fatalf("a chat without its own mode follows the agent: %q, %v", body.PermissionMode, err)
	}
	body = aiInvocationInput{AgentID: helper.ID, PermissionMode: "auto"}
	if err := admitPermissionMode(ctx, database, owner.ID, &body); err != nil || body.PermissionMode != "auto" {
		t.Fatalf("the turn's own mode wins: %q, %v", body.PermissionMode, err)
	}
}
