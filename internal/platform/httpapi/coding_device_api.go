package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// CodingApproval asks the person before a change a coding tool wants, with the
// same rules and card as Misty's own actions. The device's bridge calls it only
// when its own decision is to ask: POST /me/coding/{invocationID}/approvals
// {lease_id, token, id, action, tool, path, command, repo}. The answer is allow,
// deny or pending with the card's id, which the device then watches.
func (s *SpacesService) CodingApproval() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		if !requireCoding(w, r, s.database, user, "coding.approval") {
			return
		}
		var body struct {
			LeaseID string `json:"lease_id"`
			Token   string `json:"token"`
			ID      string `json:"id"`
			Action  string `json:"action"`
			Tool    string `json:"tool"`
			Path    string `json:"path"`
			Command string `json:"command"`
			Repo    string `json:"repo"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		run, err := s.codingRun(r.Context(), user, chi.URLParam(r, "invocationID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if _, err := s.database.CodingLeaseByToken(r.Context(), user, run.record.ID, body.LeaseID, body.Token); err != nil {
			writeSpaceError(w, err)
			return
		}
		need, title := codingGrantNeed(run, body.Action, strings.TrimSpace(body.Repo), body.Tool, body.Path, body.Command)
		summary := strings.TrimSpace(strings.Join([]string{body.Command, body.Path}, "\n"))
		approval, waiting, err := s.confirmAgentAction(r.Context(), run.invocation(), need, "coding."+body.Action,
			[]string{run.record.ID, body.ID}, title, summary)
		switch {
		case err != nil:
			writeJSON(w, http.StatusOK, map[string]any{"decision": "deny", "message": err.Error()})
		case waiting != nil:
			pending, _ := s.database.AgentAppRequest(r.Context(), user, approvalRequestID(waiting))
			id := ""
			if pending != nil {
				id = pending.ID
			}
			writeJSON(w, http.StatusOK, map[string]any{"decision": "pending", "request_id": id})
		default:
			_ = s.useAgentActionApproval(r.Context(), user, approval)
			writeJSON(w, http.StatusOK, map[string]any{"decision": "allow"})
		}
	}
}

func approvalRequestID(waiting []byte) string {
	var result struct {
		Request string `json:"request"`
	}
	_ = json.Unmarshal(waiting, &result)
	return result.Request
}

// codingGrantNeed is what a coding tool's action would need: edits and
// commands in the project, installs in the project, or a push to its remote.
func codingGrantNeed(run *codingRun, action, repo, tool, path, command string) (grantNeed, string) {
	project := run.body.CodingProject
	switch action {
	case "git.push":
		if repo == "" {
			return grantNeed{action: serveragent.GrantGitPush}, "Push commits"
		}
		return grantNeed{action: serveragent.GrantGitPush, scopeKind: serveragent.ScopeRepo, scopeID: repo, label: "push to " + repo}, "Push to " + repo
	case "project.install":
		return grantNeed{action: serveragent.GrantProjectInstall, scopeKind: serveragent.ScopeProject, scopeID: project, label: "install packages in this project"},
			"Install: " + truncateAgentRuntimeText(command, 160)
	}
	title := "Change files in the project"
	if command != "" {
		title = "Run: " + truncateAgentRuntimeText(command, 160)
	} else if path != "" {
		title = "Edit " + truncateAgentRuntimeText(path, 160)
	}
	return grantNeed{action: serveragent.GrantProjectEdit, scopeKind: serveragent.ScopeProject, scopeID: project, label: "edit files in this project"}, title
}

// CodingEnginesReport saves which coding tools a device has:
// PUT /me/coding/engines {device_id, engines:[{engine, installed, signed_in, version}]}.
// Each installed tool gets its managed agent.
func (s *SpacesService) CodingEnginesReport() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		if !requireCoding(w, r, s.database, user, "coding.engines") {
			return
		}
		var body struct {
			DeviceID string            `json:"device_id"`
			Engines  []db.CodingEngine `json:"engines"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.SaveCodingEngines(r.Context(), user, body.DeviceID, body.Engines); err != nil {
			writeSpaceError(w, err)
			return
		}
		for _, engine := range body.Engines {
			if engine.Installed {
				if _, err := s.database.EnsureEngineAgent(r.Context(), user, engine.Engine); err != nil {
					writeSpaceError(w, err)
					return
				}
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// CodingAllowance is the account's weekly coding allowance:
// GET /me/coding/allowance.
func (s *SpacesService) CodingAllowance() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		entitlements, err := s.database.EntitlementsForUser(r.Context(), user)
		if err != nil {
			writeBillingError(w, err)
			return
		}
		used, err := s.database.WeeklyCodingUsed(r.Context(), user)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"available": entitlements.Coding && entitlements.WeeklyExternalCodingAllowance > 0, "coding": entitlements.Coding, "plan": entitlements.Plan,
			"allowanceTokens": entitlements.WeeklyExternalCodingAllowance, "usedTokens": used, "resetsAt": codingWeekResets(),
		})
	}
}
