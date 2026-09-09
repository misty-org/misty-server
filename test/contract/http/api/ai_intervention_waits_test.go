package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAIInterventionMCPAndTrustedControl(t *testing.T) {
	testInterventionMCPAndTrustedControl(t, false)
}
func TestSpaceInterventionMCPAndTrustedControl(t *testing.T) {
	testInterventionMCPAndTrustedControl(t, true)
}
func testInterventionMCPAndTrustedControl(t *testing.T, spaceRun bool) {
	database := openPresenceTestDatabase(t)
	owner, err := database.CreateUser("Browser reviewer", uniqueTestEmail("browser-approval"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(t.Context(), owner.ID, "Browser review")
	if err != nil {
		t.Fatal(err)
	}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	device, err := database.RegisterTrustedDevice(owner.ID, "Review Mac", base64.RawURLEncoding.EncodeToString(publicKey), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{"browser_tools":true}`))
	if err != nil {
		t.Fatal(err)
	}
	id, runtime, scope, _ := "invocation_"+uuid.NewString(), "browser-review-"+uuid.NewString(), "scope-browser-review", uuid.NewString()
	if spaceRun {
		agent, err := database.EnsureAskIdentity(t.Context(), owner.ID, serveragent.InitialSelectedModelID)
		if err != nil {
			t.Fatal(err)
		}
		run, err := database.CreateCreatorAgentRun(t.Context(), owner.ID, space.ID, agent.ID, db.CreatorAgentRunInput{Instruction: "Read the original browser after sign-in", Mode: "auto"})
		if err != nil {
			t.Fatal(err)
		}
		id = run.ID
		if _, err := database.AttachAgentRunContext(t.Context(), owner.ID, id, device.ID, "browser_tab", scope, "Personal browser", json.RawMessage(`["browser.inspect"]`), json.RawMessage(`{"kind":"browser_tab"}`)); err != nil {
			t.Fatal(err)
		}
		jobs, err := database.ClaimPersonalAgentTaskRunJobs(t.Context(), "intervention-worker", 1, time.Minute)
		if err != nil || len(jobs) != 1 || jobs[0].Run.ID != id {
			t.Fatalf("claim: %#v %v", jobs, err)
		}
		if _, err := database.ActivatePersonalAgentTaskRuntime(t.Context(), id, "vercel-workflow", runtime); err != nil {
			t.Fatal(err)
		}
	} else {
		payload, _ := json.Marshal(map[string]any{"mode": "quick", "surface_id": "global", "trigger": "message", "space_id": space.ID, "prompt": "Inspect the attached browser page, click the Save control, then fill its form", "context": []any{map[string]any{"kind": "space.chat", "id": space.ID, "space_id": space.ID, "title": "Browser review", "privacy": "shared"}}, "timezone": "UTC", "idempotency_key": uuid.NewString()})
		if _, _, err := database.CreateAIInvocationRecord(t.Context(), db.AIInvocationRecord{ID: id, UserID: owner.ID, SpaceID: space.ID, SurfaceID: "global", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: payload, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		_, err = database.AttachAIInvocationContext(t.Context(), owner.ID, id, space.ID, device.ID, "browser_tab", scope, "Personal browser", json.RawMessage(`["browser.inspect","browser.click","browser.interact"]`), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.ActivateAIInvocationRuntime(t.Context(), id, "vercel-workflow", runtime); err != nil {
			t.Fatal(err)
		}
	}
	secret := []byte(strings.Repeat("s", 32))
	t.Setenv("MISTY_AGENT_RUNTIME_URL", "https://runtime.test")
	t.Setenv("MISTY_AGENT_RUNTIME_INTERNAL_API_URL", "https://api.test")
	t.Setenv("MISTY_AGENT_RUNTIME_CONTROL_SECRET", base64.StdEncoding.EncodeToString(secret))
	config, err := AgentRuntimeConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewSpacesService(database, nil, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	service.SetAgentRuntime(config)
	now := time.Now()
	token, err := TestingSignMCPAccessToken(secret, owner.ID, id, runtime, uuid.NewString(), "", now, now.Add(5*time.Minute), true)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.MistyMCP())
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "browser-review-proof", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token, base: http.DefaultTransport}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	oldToken, err := TestingSignMCPAccessToken(secret, owner.ID, id, runtime, uuid.NewString(), "", now, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	oldClient := mcp.NewClient(&mcp.Implementation{Name: "old-runtime", Version: "1"}, nil)
	oldSession, err := oldClient.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: bearerRoundTripper{token: oldToken, base: http.DefaultTransport}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer oldSession.Close()
	oldCatalog, err := oldSession.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range oldCatalog.Tools {
		if tool.Name == "browser.request_user_action" {
			t.Fatal("old runtime advertised an unsupported wait")
		}
	}
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range listed.Tools {
		if tool.Name == "browser.request_user_action" {
			found = true
		}
	}
	if !found {
		t.Fatal("user-action capability missing from granted browser discovery")
	}
	call := func(hook string) *mcp.CallToolResult {
		t.Helper()
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "browser.request_user_action", Arguments: map[string]any{"scopeId": scope, "action": "sign_in", "reason": "Sign in to the original personal inbox"}, Meta: mcp.Meta{"misty/call_id": "wait-for-login", "misty/device_hook_token": hook}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	waiting := call("login-hook")
	encoded, _ := json.Marshal(waiting.Meta["misty/intervention_wait"])
	var wait db.AIInterventionWait
	if waiting.IsError || json.Unmarshal(encoded, &wait) != nil || wait.ID == "" {
		t.Fatalf("missing durable wait: %#v", waiting)
	}
	repeated := call("login-hook")
	repeatedRaw, _ := json.Marshal(repeated.Meta["misty/intervention_wait"])
	var repeatedWait db.AIInterventionWait
	if repeated.IsError || json.Unmarshal(repeatedRaw, &repeatedWait) != nil || repeatedWait.ID != wait.ID {
		t.Fatalf("lost response replay: %#v", repeated)
	}
	if spaceRun {
		blocked, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "browser.inspect", Arguments: map[string]any{"scopeId": scope}, Meta: mcp.Meta{"misty/call_id": "during-wait"}})
		if err == nil && !blocked.IsError {
			t.Fatalf("executed another tool during wait: %#v", blocked)
		}
	}
	router := chi.NewRouter()
	router.Get("/me/agent-interventions", service.AIUserInterventionControl())
	router.Post("/me/agent-interventions/{waitID}", service.AIUserInterventionControl())
	account := newConversationTestBearerToken(t, database, owner.ID)
	review := performConversationRequest(t, router, http.MethodGet, "/me/agent-interventions", account, nil)
	if review.Code != 200 || !strings.Contains(review.Body.String(), wait.ID) || strings.Contains(review.Body.String(), "login-hook") {
		t.Fatalf("pending review: %d %s", review.Code, review.Body)
	}
	self := performConversationRequest(t, router, http.MethodPost, "/me/agent-interventions/"+wait.ID, token, map[string]any{"ready": true})
	if self.Code != 401 && self.Code != 403 {
		t.Fatalf("runtime approved its own wait: %d %s", self.Code, self.Body)
	}
	decision := performConversationRequest(t, router, http.MethodPost, "/me/agent-interventions/"+wait.ID, account, map[string]any{"ready": true})
	if decision.Code != 202 {
		t.Fatalf("trusted decision: %d %s", decision.Code, decision.Body)
	}
	result := call("resumed-login-hook")
	data, _ := json.Marshal(result.StructuredContent)
	if result.IsError || !strings.Contains(string(data), `"requiresFreshInspection":true`) {
		t.Fatalf("original action not resumed: %#v", result)
	}
}
