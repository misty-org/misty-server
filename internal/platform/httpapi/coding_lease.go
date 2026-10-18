package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// A lease covers at most this many universal tokens and this long; the device
// asks again at a turn boundary.
const (
	codingLeaseChunk = 400_000
	codingLeaseTTL   = 30 * time.Minute
)

// codingRun is a delegated coding run the person owns, with what it needs.
type codingRun struct {
	record *db.AIInvocationRecord
	body   aiInvocationInput
	parent *db.AIInvocationRecord
}

func (s *SpacesService) codingRun(ctx context.Context, user, id string) (*codingRun, error) {
	record, err := s.database.AIInvocationByID(ctx, user, id)
	if err != nil {
		return nil, db.ErrSpaceNotFound
	}
	run := &codingRun{record: record}
	if json.Unmarshal(record.RequestPayload, &run.body) != nil || run.body.ExecutionMode != codingExecutionMode || run.body.DelegatedFrom == "" {
		return nil, db.ErrSpaceNotFound
	}
	if aiInvocationTerminal(record.State) {
		return nil, db.ErrSpaceConflict
	}
	if run.parent, err = s.database.AIInvocationByID(ctx, user, run.body.DelegatedFrom); err != nil {
		return nil, db.ErrSpaceNotFound
	}
	return run, nil
}

// codingInvocation is how the delegating agent acts for this run: its grants
// cover the run, and only this run. Approval cards show in the parent's chat.
func (run *codingRun) invocation() agenttools.Invocation {
	return agenttools.Invocation{UserID: run.record.UserID, AgentID: run.body.AgentID, RunID: run.parent.ID, PermissionMode: run.body.PermissionMode}
}

// codingGrants are the actions the person remembered for this project and its
// remote, for the delegating agent. Ask ignores them, so none are sent.
func (s *SpacesService) codingGrants(ctx context.Context, run *codingRun, repo string) []string {
	invocation := run.invocation()
	if s.invocationPermissionMode(ctx, invocation) != serveragent.PermissionAuto {
		return []string{}
	}
	agentID := s.grantAgentID(ctx, invocation)
	out := []string{}
	check := func(action serveragent.GrantAction, kind serveragent.ScopeKind, scope string) {
		if grant, err := s.database.ActiveAgentGrant(ctx, run.record.UserID, agentID, string(action), string(kind), scope); err == nil && grant != nil {
			out = append(out, string(action))
		}
	}
	check(serveragent.GrantProjectEdit, serveragent.ScopeProject, run.body.CodingProject)
	check(serveragent.GrantProjectInstall, serveragent.ScopeProject, run.body.CodingProject)
	if repo != "" {
		check(serveragent.GrantGitPush, serveragent.ScopeRepo, repo)
	}
	return out
}

// CodingLease issues a budget for the next turn: POST /me/coding/{invocationID}/lease
// {device_id, engine, model, repo}. It refuses plans without coding, outside
// agents, a spent allowance, and Grok or DeepSeek Shell unless the chat is in
// Auto with edits, installs and pushes remembered.
func (s *SpacesService) CodingLease() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		if !requireCoding(w, r, s.database, user, "coding.lease") {
			return
		}
		var body struct {
			DeviceID string `json:"device_id"`
			Engine   string `json:"engine"`
			Model    string `json:"model"`
			Repo     string `json:"repo"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		run, err := s.codingRun(r.Context(), user, chi.URLParam(r, "invocationID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !db.ValidCodingEngine(body.Engine) || strings.TrimSpace(body.DeviceID) == "" || len(body.Model) > 200 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_lease"})
			return
		}
		grants := s.codingGrants(r.Context(), run, strings.TrimSpace(body.Repo))
		if reason := codingEngineRefusal(body.Engine, run, grants); reason != "" {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "engine_not_allowed", "message": reason})
			return
		}
		budget, err := s.codingBudget(r.Context(), user)
		if errors.Is(err, errPlanUpgradeRequired) {
			writeJSON(w, http.StatusPaymentRequired, map[string]string{"code": codePlanUpgradeRequired, "message": planUpgradeMessage})
			return
		}
		if err != nil {
			writeBillingError(w, err)
			return
		}
		lease, token, err := s.database.IssueCodingLease(r.Context(), user, db.CodingLease{
			InvocationID: run.record.ID, DeviceID: body.DeviceID, Engine: body.Engine, Model: body.Model,
			TokenBudget: budget, ExpiresAt: time.Now().Add(codingLeaseTTL),
		})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		// A new lease means the device finished its last turn: settle the old ones.
		if err := s.settleCodingLeases(r.Context(), run, lease.ID); err != nil {
			writeBillingError(w, err)
			return
		}
		if s.usageMeter != nil {
			if _, err := serveragent.ReserveMeasuredUsage(s.usageMeter, user, codingUsageKey(run.record.ID, lease.ID), codingBillingProvider(body.Engine),
				firstAIText(body.Model, body.Engine), map[string]int64{"external_tokens": budget}, "coding:"+run.record.ID); err != nil {
				writeBillingError(w, err)
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"lease": map[string]any{
			"invocation_id": lease.InvocationID, "lease_id": lease.ID, "token_budget": lease.TokenBudget, "expires_at": lease.ExpiresAt.Unix(),
			"grant_version": time.Now().UnixMilli(), "grants": grants, "signature": token,
			"mode": run.body.PermissionMode, "plan": run.body.CollaborationMode == db.ConversationModePlan,
		}})
	}
}

// codingUsageKey matches the runtime's turn keys so settlement reuses them.
func codingUsageKey(invocationID, leaseID string) string {
	return "agent-runtime:" + invocationID + ":model:" + leaseID
}

// codingBudget is the next lease's size: what is left of the weekly coding
// allowance, at most one chunk. Plans without coding have none.
func (s *SpacesService) codingBudget(ctx context.Context, user string) (int64, error) {
	entitlements, err := s.database.EntitlementsForUser(ctx, user)
	if err != nil {
		return 0, err
	}
	if !entitlements.Coding || entitlements.WeeklyExternalCodingAllowance <= 0 {
		return 0, errPlanUpgradeRequired
	}
	used, err := s.database.WeeklyCodingUsed(ctx, user)
	if err != nil {
		return 0, err
	}
	left := entitlements.WeeklyExternalCodingAllowance - used
	if left <= 0 {
		resets := codingWeekResets()
		return 0, &billingadapter.Denial{Limit: "weekly", ResetsAt: &resets}
	}
	if left > codingLeaseChunk {
		left = codingLeaseChunk
	}
	return left, nil
}

// codingEngineRefusal checks what the device chose against the server's rules:
// a tool that cannot ask before acting never runs where it would need to.
func codingEngineRefusal(engine string, run *codingRun, grants []string) string {
	has := func(action string) bool {
		for _, grant := range grants {
			if grant == action {
				return true
			}
		}
		return false
	}
	plan := run.body.CollaborationMode == db.ConversationModePlan
	auto := serveragent.NormalizePermissionMode(run.body.PermissionMode) == serveragent.PermissionAuto
	switch engine {
	case "grok", "dsh":
		if !auto || plan || !has("project.edit") || !has("project.install") || !has("git.push") {
			name := map[string]string{"grok": "Grok", "dsh": "DeepSeek Shell"}[engine]
			return name + " can't ask before acting. It runs only in Auto, on a project where you remembered edits, installs and pushes."
		}
	case "pi", "agy", "qoder", "qoder-cn":
		if !auto && !plan {
			return "This coding tool can't ask before acting, so in Ask it only plans."
		}
	}
	return ""
}

// codingWeekResets is when the weekly coding allowance starts over: next
// Monday, 00:00 UTC.
func codingWeekResets() time.Time {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := (8 - int(start.Weekday())) % 7
	if days == 0 {
		days = 7
	}
	return start.AddDate(0, 0, days)
}
