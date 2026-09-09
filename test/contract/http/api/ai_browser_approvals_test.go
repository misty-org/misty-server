package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAIInvocationBrowserApprovalResumesExactAction(t *testing.T) {
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
	id, runtime, scope, document := "invocation_"+uuid.NewString(), "browser-review-"+uuid.NewString(), "scope-browser-review", uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"mode": "quick", "surface_id": "global", "trigger": "message", "space_id": space.ID, "prompt": "Inspect the attached browser page, click the Save control, then fill its form", "context": []any{map[string]any{"kind": "space.chat", "id": space.ID, "space_id": space.ID, "title": "Browser review", "privacy": "shared"}}, "timezone": "UTC", "idempotency_key": uuid.NewString()})
	if _, _, err := database.CreateAIInvocationRecord(t.Context(), db.AIInvocationRecord{ID: id, UserID: owner.ID, SpaceID: space.ID, SurfaceID: "global", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: payload, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	attached, err := database.AttachAIInvocationContext(t.Context(), owner.ID, id, space.ID, device.ID, "browser_tab", scope, "Personal browser", json.RawMessage(`["browser.inspect","browser.click","browser.interact"]`), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateAIInvocationRuntime(t.Context(), id, "vercel-workflow", runtime); err != nil {
		t.Fatal(err)
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
	token, err := TestingSignMCPAccessToken(secret, owner.ID, id, runtime, uuid.NewString(), "", now, now.Add(5*time.Minute))
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
	type outcome struct {
		result *mcp.CallToolResult
		err    error
	}
	execute := func(params *mcp.CallToolParams, operation string, output json.RawMessage) *mcp.CallToolResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
		defer cancel()
		done := make(chan outcome, 1)
		go func() { result, err := session.CallTool(ctx, params); done <- outcome{result, err} }()
		var job *db.WorkflowDeviceNodeJob
		var lease string
		limit := time.Now().Add(5 * time.Second)
		for {
			job, lease, err = database.ClaimWorkflowDeviceNodeJob(owner.ID, device.ID, time.Minute, 2)
			if err == nil {
				break
			}
			if !errors.Is(err, db.ErrAgentJobNotFound) || time.Now().After(limit) {
				select {
				case got := <-done:
					t.Fatalf("device dispatch: %v; result=%#v error=%v", err, got.result, got.err)
				default:
					t.Fatal(err)
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		if job.Operation != operation || job.ContextID != attached.ID || job.ScopeID != scope {
			t.Fatalf("wrong browser binding: %#v", job)
		}
		if _, err := database.BeginWorkflowDeviceNodeJob(owner.ID, device.ID, job.ID, lease); err != nil {
			t.Fatal(err)
		}
		if _, err := database.FinishWorkflowDeviceNodeJob(owner.ID, device.ID, job.ID, lease, "completed", output, ""); err != nil {
			t.Fatal(err)
		}
		got := <-done
		if got.err != nil || got.result == nil || got.result.IsError {
			t.Fatalf("browser result: %#v %v", got.result, got.err)
		}
		return got.result
	}
	snapshot := json.RawMessage(`{"documentId":"` + document + `","url":"https://example.org/form","title":"Personal form","text":"A form","interactive":[{"ref":"ref-save","name":"Save <script>page hint</script>"}],"truncated":false}`)
	execute(&mcp.CallToolParams{Name: "browser.inspect", Arguments: map[string]any{"scopeId": scope}, Meta: mcp.Meta{"misty/call_id": "inspect-first"}}, "browser.inspect", snapshot)
	router := chi.NewRouter()
	router.Get("/me/capability-approvals/{approvalID}", service.SDKCapabilityApprovalReview())
	router.Post("/me/sdk-runs/{runID}/approvals/{approvalID}", service.SDKCapabilityApproval())
	account := newConversationTestBearerToken(t, database, owner.ID)
	other, err := database.CreateUser("Other reviewer", uniqueTestEmail("other-browser-review"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	otherToken := newConversationTestBearerToken(t, database, other.ID)
	for index, operation := range []string{"browser.click", "browser.interact"} {
		if index > 0 {
			fresh := uuid.NewString()
			snapshot = json.RawMessage(strings.ReplaceAll(string(snapshot), document, fresh))
			document = fresh
			execute(&mcp.CallToolParams{Name: "browser.inspect", Arguments: map[string]any{"scopeId": scope}, Meta: mcp.Meta{"misty/call_id": "inspect-after-click"}}, "browser.inspect", snapshot)
		}
		args := map[string]any{"scopeId": scope, "elementRef": "ref-save"}
		if operation == "browser.interact" {
			args = map[string]any{"scopeId": scope, "documentId": document, "action": map[string]any{"kind": "fill", "elementRef": "ref-save", "text": "Reviewed text"}}
		}
		callID := operation + "-exact"
		params := &mcp.CallToolParams{Name: operation, Arguments: args, Meta: mcp.Meta{"misty/call_id": callID, "misty/approval_hook_token": callID + "-hook"}}
		pending, err := session.CallTool(t.Context(), params)
		if err != nil || pending.IsError || pending.Meta["misty/approval"] == nil {
			t.Fatalf("missing %s approval: %#v %v", operation, pending, err)
		}
		raw, _ := json.Marshal(pending.Meta["misty/approval"])
		var approval struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &approval) != nil || approval.ID == "" {
			t.Fatalf("invalid review: %s", raw)
		}
		repeated, err := session.CallTool(t.Context(), params)
		repeatRaw, _ := json.Marshal(repeated.Meta["misty/approval"])
		if err != nil || repeated.IsError || !strings.Contains(string(repeatRaw), approval.ID) {
			t.Fatalf("pending replay changed approval: %s %v", repeatRaw, err)
		}
		if job, _, err := database.ClaimWorkflowDeviceNodeJob(owner.ID, device.ID, time.Minute, 2); !errors.Is(err, db.ErrAgentJobNotFound) {
			t.Fatalf("dispatched without approval: %#v %v", job, err)
		}
		reviewPath := "/me/capability-approvals/" + approval.ID
		response := performConversationRequest(t, router, http.MethodGet, reviewPath, account, nil)
		var reviewed struct {
			Review struct {
				Kind         string         `json:"kind"`
				PageURL      string         `json:"pageUrl"`
				ElementLabel string         `json:"elementLabel"`
				Input        map[string]any `json:"input"`
			} `json:"review"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &reviewed) != nil || reviewed.Review.Kind != "browser" || reviewed.Review.PageURL != "https://example.org/form" || reviewed.Review.ElementLabel != "Save <script>page hint</script>" || reviewed.Review.Input["scopeId"] != scope {
			t.Fatalf("wrong review: %d %s", response.Code, response.Body.String())
		}
		for _, deniedToken := range []string{token, otherToken} {
			denied := performConversationRequest(t, router, http.MethodGet, reviewPath, deniedToken, nil)
			if denied.Code != 401 && denied.Code != 403 {
				t.Fatalf("unauthorized review: %d %s", denied.Code, denied.Body.String())
			}
		}
		decisionPath := "/me/sdk-runs/" + strings.TrimPrefix(id, "invocation_") + "/approvals/" + approval.ID
		self := performConversationRequest(t, router, http.MethodPost, decisionPath, token, map[string]any{"approved": true})
		if self.Code != 401 && self.Code != 403 {
			t.Fatalf("self approval: %d %s", self.Code, self.Body.String())
		}
		decision := performConversationRequest(t, router, http.MethodPost, decisionPath, account, map[string]any{"approved": true})
		if decision.Code != http.StatusNoContent {
			t.Fatalf("approval: %d %s", decision.Code, decision.Body.String())
		}
		if index == 0 {
			online := func(value bool) {
				t.Helper()
				offset := "-2 minutes"
				if value {
					offset = "0 minutes"
				}
				if err := database.TestingSpaceTx(t.Context(), func(tx *sql.Tx) error {
					_, err := tx.ExecContext(t.Context(), `UPDATE trusted_devices SET last_seen_at=NOW()+$2::interval WHERE id=$1`, device.ID, offset)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			waitForDevice := func(hook string) {
				t.Helper()
				params.Meta["misty/device_hook_token"] = hook
				pending, err := session.CallTool(t.Context(), params)
				if err != nil || pending.IsError || pending.Meta["misty/device_wait"] != true {
					t.Fatalf("device wait: %#v %v", pending, err)
				}
			}
			resume := func(hook string) db.AgentContinuation {
				t.Helper()
				waits, err := database.AIInvocationDeviceWaitsReady(t.Context(), 20)
				if err != nil || len(waits) != 1 || waits[0].HookToken != hook || !waits[0].Available {
					t.Fatalf("wrong ready target: %#v %v", waits, err)
				}
				if err := database.QueueAgentDeviceResume(t.Context(), waits[0]); err != nil {
					t.Fatal(err)
				}
				var raw []byte
				if err := database.TestingSpaceTx(t.Context(), func(tx *sql.Tx) error {
					return tx.QueryRowContext(t.Context(), `SELECT payload FROM agent_runtime_deliveries WHERE run_id=$1 AND operation='device.resume' AND payload->>'hook_token'=$2`, id, hook).Scan(&raw)
				}); err != nil {
					t.Fatal(err)
				}
				var payload db.AgentContinuation
				if json.Unmarshal(raw, &payload) != nil || payload.RuntimeID != runtime || !payload.Available {
					t.Fatalf("missing durable continuation: %s", raw)
				}
				return payload
			}
			online(false)
			waitForDevice("offline-first")
			waitForDevice("offline-first")
			budget, err := database.AgentRunExecutionBudget(t.Context(), owner.ID, id, runtime, false)
			if err != nil || budget.Active {
				t.Fatalf("wait consumed active clock: %#v %v", budget, err)
			}
			if waits, err := database.AIInvocationDeviceWaitsReady(t.Context(), 20); err != nil || len(waits) != 0 {
				t.Fatalf("offline target resumed: %#v %v", waits, err)
			}
			online(true)
			old := resume("offline-first")
			delivery := db.AgentRuntimeDelivery{RunID: id, UserID: owner.ID, Operation: "device.resume"}
			if allowed, err := database.AIInvocationDeviceResumeAuthorized(t.Context(), delivery, old); err != nil || !allowed {
				t.Fatalf("authorized device blocked: %v %v", allowed, err)
			}
			// The device can sleep again after its resume was queued. The second wait
			// retains the original approved effect and survives a late first receipt.
			online(false)
			waitForDevice("offline-second")
			if err := database.FinishAgentContinuation(t.Context(), delivery, old); err != nil {
				t.Fatal(err)
			}
			waitForDevice("offline-second")
			if current, err := database.AgentContinuationCurrent(t.Context(), delivery, old); err != nil || current {
				t.Fatalf("stale device continuation became current: %v %v", current, err)
			}
			online(true)
			resume("offline-second")
		}
		execute(params, operation, json.RawMessage(`{"attempted":true}`))
		replay, err := session.CallTool(t.Context(), params)
		if err != nil || replay.IsError {
			t.Fatalf("confirmed replay: %#v %v", replay, err)
		}
		if job, _, err := database.ClaimWorkflowDeviceNodeJob(owner.ID, device.ID, time.Minute, 2); !errors.Is(err, db.ErrAgentJobNotFound) {
			t.Fatalf("repeated effect: %#v %v", job, err)
		}
		if index == 0 {
			args["elementRef"] = "another-control"
			changed, err := session.CallTool(t.Context(), params)
			if err == nil && !changed.IsError {
				t.Fatalf("changed approved action accepted: %#v", changed)
			}
		}
	}
}
