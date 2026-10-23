package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"github.com/kannachi323/misty/server/test/testkit"
)

// planBilling answers billing summaries with each account's plan.
func planBilling(t *testing.T, plans map[string]db.Tier) {
	t.Helper()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req billingadapter.Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		plan, ok := plans[req.AccountID]
		if !ok {
			plan = db.TierBasic
		}
		allowance := int64(20_000_000)
		if plan == db.TierBasic {
			allowance = 0
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"allowed": true, "summary": map[string]any{
			"tier":         plan,
			"entitlements": db.PlanEntitlements{Plan: plan, MaxOwnedSpaces: 3, WeeklyExternalCodingAllowance: allowance},
		}})
	}))
	t.Cleanup(endpoint.Close)
	t.Setenv("MISTY_ENVIRONMENT", "development")
	t.Setenv("MISTY_BILLING_ADAPTER", "http")
	t.Setenv("MISTY_BILLING_URL", endpoint.URL)
	t.Setenv("MISTY_BILLING_SECRET", "coding-plan-test-secret-0000000000000")
	TestingResetCodingPlans()
	t.Cleanup(TestingResetCodingPlans)
}

func TestCodingPlanGatePostgres(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := t.Context()
	basic, _ := database.CreateUser("coding-basic@example.test", "password123")
	pro, _ := database.CreateUser("coding-pro@example.test", "password123")
	ultra, _ := database.CreateUser("coding-ultra@example.test", "password123")
	planBilling(t, map[string]db.Tier{basic.ID: db.TierBasic, pro.ID: db.TierPro, ultra.ID: db.TierUltraPlus})

	service := &SpacesService{database: database, aiInvocations: newAIInvocationHub(database)}
	router := chi.NewRouter()
	router.Get("/integrations/github/repos", service.GitHubRepos())
	router.Post("/integrations/github/repos", service.GitHubRepos())
	router.Post("/me/agent-grants/project", service.AgentProjectGrants())
	router.Put("/me/coding/engines", service.CodingEnginesReport())
	router.Post("/me/coding/{invocationID}/lease", service.CodingLease())
	router.Post("/me/coding/{invocationID}/events", service.CodingEvents())
	router.Post("/me/coding/{invocationID}/approvals", service.CodingApproval())
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	send := func(user, method, target, body string, headers map[string]string) (int, string) {
		t.Helper()
		cookie, _ := signer.Mint(user, "session-"+user, "access", time.Now().Add(time.Hour))
		r := httptest.NewRequestWithContext(ctx, method, target, bytes.NewReader([]byte(body)))
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: cookie})
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("User-Agent", "MistyDesktop/0.4.0 (macos)")
		r.Header.Set("X-Misty-CSRF", "1")
		for name, value := range headers {
			r.Header.Set(name, value)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		var answer struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &answer)
		return w.Code, answer.Code
	}
	entries := []struct{ method, target, body string }{
		{http.MethodGet, "/integrations/github/repos", ""},
		{http.MethodPost, "/integrations/github/repos", `{"name":"site"}`},
		{http.MethodPost, "/me/agent-grants/project", `{"scope_id":"scope_site","actions":["project.edit","git.push"],"repo":"ana/site"}`},
		{http.MethodPut, "/me/coding/engines", `{"device_id":"device_x","engines":[]}`},
		{http.MethodPost, "/me/coding/invocation_x/lease", `{"device_id":"device_x","engine":"claude"}`},
		{http.MethodPost, "/me/coding/invocation_x/events", `{"device_id":"device_x","events":[]}`},
		{http.MethodPost, "/me/coding/invocation_x/approvals", `{"action":"git.push","repo":"ana/site"}`},
	}
	for _, entry := range entries {
		name := entry.method + " " + entry.target
		// Basic is refused, whatever the request goes on to ask.
		if status, code := send(basic.ID, entry.method, entry.target, entry.body, nil); status != http.StatusPaymentRequired || code != codePlanUpgradeRequired {
			t.Errorf("Basic at %s = %d %q", name, status, code)
		}
		// Pro and above pass the gate and reach the endpoint's own checks.
		for _, user := range []string{pro.ID, ultra.ID} {
			if status, code := send(user, entry.method, entry.target, entry.body, nil); status == http.StatusPaymentRequired || code == codePlanUpgradeRequired || code == codeOutsideAgentBlocked {
				t.Errorf("paid plan at %s = %d %q", name, status, code)
			}
		}
		// An outside agent is blocked even on a paid plan.
		if status, code := send(pro.ID, entry.method, entry.target, entry.body, map[string]string{"User-Agent": "curl/8.7.1"}); status != http.StatusForbidden || code != codeOutsideAgentBlocked {
			t.Errorf("curl at %s = %d %q", name, status, code)
		}
		if status, code := send(pro.ID, entry.method, entry.target, entry.body, map[string]string{"X-Misty-Automation": "webdriver"}); status != http.StatusForbidden || code != codeOutsideAgentBlocked {
			t.Errorf("WebDriver at %s = %d %q", name, status, code)
		}
	}

	// Attaching a project to a run.
	ai := &AIService{database: database}
	admit := func(user string, headers map[string]string) (int, string) {
		t.Helper()
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/ai/invocations", nil)
		r.Header.Set("User-Agent", "MistyDesktop/0.4.0 (macos)")
		r.Header.Set("X-Misty-CSRF", "1")
		for name, value := range headers {
			r.Header.Set(name, value)
		}
		w := httptest.NewRecorder()
		body := aiInvocationInput{Mode: "drawer", SurfaceID: "global", Prompt: "Fix it", DeviceContexts: []aiInvocationDeviceContext{
			{DeviceID: "device_x", Kind: "project_folder", OpaqueRef: "scope_site", Capabilities: json.RawMessage(`["code.delegate"]`)},
		}}
		ai.admitInvocationExecution(w, r, user, &body, &aiInvocationAdmitState{})
		var answer struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &answer)
		return w.Code, answer.Code
	}
	if status, code := admit(basic.ID, nil); status != http.StatusPaymentRequired || code != codePlanUpgradeRequired {
		t.Fatalf("Basic attaching a project = %d %q", status, code)
	}
	if status, code := admit(pro.ID, nil); code == codePlanUpgradeRequired || code == codeOutsideAgentBlocked {
		t.Fatalf("Pro attaching a project = %d %q", status, code)
	}
	if status, code := admit(pro.ID, map[string]string{"User-Agent": "python-httpx/0.27"}); status != http.StatusForbidden || code != codeOutsideAgentBlocked {
		t.Fatalf("a script attaching a project = %d %q", status, code)
	}

	// The agent toolbox: coding tools are left out for Basic, and a call that
	// names them anyway is refused without starting anything.
	projects := []deviceGrant{{scopeID: "scope_site", name: "site", deviceID: "device_x", deviceName: "Studio Mac"}}
	run := func(user string) *db.AIInvocationRecord {
		t.Helper()
		payload, _ := json.Marshal(aiInvocationInput{Mode: "drawer", SurfaceID: "global", Trigger: "message", Prompt: "Fix it", Timezone: "UTC"})
		record, _, err := database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{
			ID: "invocation_" + uuid.NewString(), UserID: user, SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "queued",
			IdempotencyKey: uuid.NewString(), RequestPayload: payload, ExpiresAt: time.Now().Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		return &record
	}
	basicRun, proRun := run(basic.ID), run(pro.ID)
	if tools := service.codeDelegationToolRegistrations(ctx, basicRun, aiInvocationInput{}, projects, collaborationState{}); len(tools) != 0 {
		t.Fatalf("Basic catalog has coding tools: %d", len(tools))
	}
	if tools := service.codeDelegationToolRegistrations(ctx, proRun, aiInvocationInput{}, projects, collaborationState{}); len(tools) != 2 {
		t.Fatalf("Pro catalog coding tools = %d", len(tools))
	}
	raw, err := service.delegateCoding(ctx, basicRun, aiInvocationInput{}, projects, collaborationState{mode: db.ConversationModeAct}, agent.ToolRequest{ID: "call-1", Arguments: json.RawMessage(`{"task":"Fix the parser"}`)})
	if err != nil || !strings.Contains(string(raw), codePlanUpgradeRequired) {
		t.Fatalf("Basic code_delegate = %s, %v", raw, err)
	}
	if children, _ := database.DelegationChildren(ctx, basic.ID, basicRun.ID, "call-1"); len(children) != 0 {
		t.Fatalf("Basic code_delegate started %d runs", len(children))
	}

	// The lease's own budget check agrees with the plan.
	if _, err := service.codingBudget(ctx, basic.ID); err != errPlanUpgradeRequired {
		t.Fatalf("Basic budget = %v", err)
	}
	if budget, err := service.codingBudget(ctx, pro.ID); err != nil || budget <= 0 {
		t.Fatalf("Pro budget = %d, %v", budget, err)
	}
}
