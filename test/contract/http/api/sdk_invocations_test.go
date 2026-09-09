package api

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sdkFixtureTransport func(*http.Request) (*http.Response, error)

func (f sdkFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testSDKInvocationHTTPExecution(t *testing.T, database *db.Database, service *SpacesService, router *chi.Mux, appToken, accountToken, targetID string) {
	t.Helper()
	t.Setenv("MISTY_SDK_EXECUTION_ENABLED", "true")
	secret := []byte(strings.Repeat("s", 32))
	t.Setenv("MISTY_AGENT_RUNTIME_URL", "https://runtime.test")
	t.Setenv("MISTY_AGENT_RUNTIME_INTERNAL_API_URL", "https://api.test")
	t.Setenv("MISTY_AGENT_RUNTIME_CONTROL_SECRET", base64.StdEncoding.EncodeToString(secret))
	config, err := AgentRuntimeConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	service.SetAgentRuntime(config)
	router.Post("/capabilities/invocations", service.InvokeSDKCapability())
	router.Get("/capabilities/invocations/{requestID}", service.SDKCapabilityResult())
	router.Post("/capabilities/invocations/{requestID}/cancel", service.CancelSDKCapability())
	router.Post("/runtime/{runID}/complete", service.AgentRuntimeComplete())
	router.Post("/me/sdk-runs/{runID}/approvals/{approvalID}", service.SDKCapabilityApproval())
	request := cap.Invocation{RequestID: uuid.NewString(), Capability: "habits.list", CapabilityVersion: 1, ProviderID: "example.habits/backend", ProviderVersion: 1, TargetID: targetID, TargetRevision: 1, Input: json.RawMessage(`{}`), Deadline: time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)}
	response := performConversationRequest(t, router, http.MethodPost, "/capabilities/invocations", appToken, request)
	var accepted struct {
		RunID string `json:"runId"`
	}
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &accepted) != nil || accepted.RunID == "" {
		t.Fatalf("admit: %d %s", response.Code, response.Body.String())
	}
	response = performConversationRequest(t, router, http.MethodPost, "/capabilities/invocations", appToken, request)
	var duplicate struct {
		RunID string `json:"runId"`
	}
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &duplicate) != nil || duplicate.RunID != accepted.RunID {
		t.Fatalf("duplicate request: %d %s", response.Code, response.Body.String())
	}
	invocationID := "invocation_" + accepted.RunID
	runtimeID := "sdk-fixture-" + uuid.NewString()
	record, err := database.ActivateAIInvocationRuntime(t.Context(), invocationID, "vercel-workflow", runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := database.SDKInvocationForRun(t.Context(), record.UserID, invocationID)
	if err != nil {
		t.Fatal(err)
	}
	var backendCalls atomic.Int32
	var loseResponse atomic.Bool
	service.TestingSetSDKBackendClientFactory(func(endpoint, bearer string) (*http.Client, error) {
		if endpoint != "https://habits.example.com/execute" || bearer != "private-provider-token" {
			t.Error("connection did not use pinned private credentials")
		}
		return &http.Client{Transport: sdkFixtureTransport(func(r *http.Request) (*http.Response, error) {
			backendCalls.Add(1)
			if loseResponse.Load() {
				return nil, io.ErrUnexpectedEOF
			}
			var payload struct {
				Protocol  int           `json:"protocol"`
				Execution cap.Execution `json:"execution"`
				Target    cap.Target    `json:"target"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload.Protocol != 1 || payload.Execution.EffectID != invocation.EffectID || payload.Execution.RunID != accepted.RunID || payload.Target.ID != targetID || r.Header.Get("Idempotency-Key") != invocation.EffectID {
				t.Error("backend identity was not pinned")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"status":"success","result":["walked"],"partial":false,"evidence":[]}`))}, nil
		})}, nil
	})
	now := time.Now().UTC()
	token, err := TestingSignMCPAccessToken(secret, record.UserID, invocationID, runtimeID, "sdk-contract-token", "", now, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.MistyMCP())
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "sdk-proof", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token, base: http.DefaultTransport}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	catalog, err := session.ListTools(t.Context(), nil)
	if err != nil || len(catalog.Tools) != 1 {
		t.Fatalf("pinned catalog: %#v %v", catalog, err)
	}
	tool := catalog.Tools[0]
	params := &mcp.CallToolParams{Name: tool.Name, Arguments: map[string]any{}, Meta: mcp.Meta{"misty/call_id": invocation.EffectID, "misty/approval_hook_token": "sdk-read-hook"}}
	result, err := session.CallTool(t.Context(), params)
	if err != nil || result.IsError {
		t.Fatalf("SDK backend call: %#v %v", result, err)
	}
	result, err = session.CallTool(t.Context(), params)
	if err != nil || result.IsError || backendCalls.Load() != 1 {
		t.Fatalf("confirmed replay: %#v calls=%d %v", result, backendCalls.Load(), err)
	}
	params.Meta["misty/call_id"] = uuid.NewString()
	result, err = session.CallTool(t.Context(), params)
	if err == nil && !result.IsError {
		t.Fatal("accepted substituted effect identity")
	}
	if backendCalls.Load() != 1 {
		t.Fatal("replayed backend effect")
	}
	complete := func(runID, runtime string) {
		t.Helper()
		path := "/runtime/" + runID + "/complete"
		body, _ := json.Marshal(map[string]any{"runtime_run_id": runtime, "status": "success", "text": "Done"})
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "complete:"+runID)
		r.Header.Set("X-Misty-Agent-Timestamp", stamp)
		r.Header.Set("X-Misty-Agent-Signature", TestingAgentRuntimeSignature(secret, http.MethodPost, path, stamp, body))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("complete: %d %s", w.Code, w.Body.String())
		}
	}
	testSDKRuntimeRecovery(t, database, service, config, record.UserID, invocationID, runtimeID)
	complete(invocationID, runtimeID)
	complete(invocationID, runtimeID)
	response = performConversationRequest(t, router, http.MethodGet, "/capabilities/invocations/"+request.RequestID, appToken, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"state":"completed"`) || !strings.Contains(response.Body.String(), "walked") || strings.Contains(response.Body.String(), "private-provider-token") {
		t.Fatalf("confirmed result: %d %s", response.Code, response.Body.String())
	}
	params.Meta["misty/call_id"] = invocation.EffectID
	if late, err := session.CallTool(t.Context(), params); err == nil && !late.IsError {
		t.Fatal("completed request still executable")
	}
	for _, lost := range []bool{false, true} {
		request.RequestID = uuid.NewString()
		request.Capability = "habits.record"
		response = performConversationRequest(t, router, http.MethodPost, "/capabilities/invocations", appToken, request)
		if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &accepted) != nil {
			t.Fatalf("write admit: %s", response.Body.String())
		}
		invocationID = "invocation_" + accepted.RunID
		runtimeID = "sdk-write-" + uuid.NewString()
		record, err = database.ActivateAIInvocationRuntime(t.Context(), invocationID, "vercel-workflow", runtimeID)
		if err != nil {
			t.Fatal(err)
		}
		invocation, err = database.SDKInvocationForRun(t.Context(), record.UserID, invocationID)
		if err != nil {
			t.Fatal(err)
		}
		token, err = TestingSignMCPAccessToken(secret, record.UserID, invocationID, runtimeID, uuid.NewString(), "", now, now.Add(5*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		writeClient := mcp.NewClient(&mcp.Implementation{Name: "sdk-write-proof", Version: "1"}, nil)
		writeSession, err := writeClient.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token, base: http.DefaultTransport}}, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer writeSession.Close()
		catalog, err = writeSession.ListTools(t.Context(), nil)
		if err != nil || len(catalog.Tools) != 1 {
			t.Fatalf("write catalog: %#v %v", catalog, err)
		}
		params = &mcp.CallToolParams{Name: catalog.Tools[0].Name, Arguments: map[string]any{}, Meta: mcp.Meta{"misty/call_id": invocation.EffectID, "misty/approval_hook_token": "sdk-write-hook"}}
		before := backendCalls.Load()
		pending, err := writeSession.CallTool(t.Context(), params)
		if err != nil || pending.IsError || pending.Meta["misty/approval"] == nil || backendCalls.Load() != before {
			t.Fatalf("manifest bypassed minimum approval: %#v %v", pending, err)
		}
		response = performConversationRequest(t, router, http.MethodGet, "/capabilities/invocations/"+request.RequestID, appToken, nil)
		var waiting struct {
			State   string `json:"state"`
			Outcome struct {
				ApprovalID string `json:"approvalId"`
			} `json:"outcome"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &waiting) != nil || waiting.State != "waiting" || waiting.Outcome.ApprovalID == "" {
			t.Fatalf("wait: %d %s", response.Code, response.Body.String())
		}
		testSDKApprovalReview(t, database, record.UserID, waiting.Outcome.ApprovalID, targetID, "", request.Input, appToken, token)
		approvalPath := "/me/sdk-runs/" + accepted.RunID + "/approvals/" + waiting.Outcome.ApprovalID
		response = performConversationRequest(t, router, http.MethodPost, approvalPath, appToken, map[string]any{"approved": true})
		if response.Code != 403 {
			t.Fatalf("app self-approved: %d %s", response.Code, response.Body.String())
		}
		response = performConversationRequest(t, router, http.MethodPost, approvalPath, accountToken, map[string]any{"approved": true})
		if response.Code != 204 {
			t.Fatalf("trusted approval: %d %s", response.Code, response.Body.String())
		}
		loseResponse.Store(lost)
		result, err = writeSession.CallTool(t.Context(), params)
		if !lost && (err != nil || result.IsError) {
			t.Fatalf("approved write: %#v %v", result, err)
		}
		// A lost reply can never cause another backend attempt for this effect.
		_, _ = writeSession.CallTool(t.Context(), params)
		if backendCalls.Load() != before+1 {
			t.Fatalf("write retried: before=%d after=%d", before, backendCalls.Load())
		}
		complete(invocationID, runtimeID)
		response = performConversationRequest(t, router, http.MethodGet, "/capabilities/invocations/"+request.RequestID, appToken, nil)
		state := "completed"
		if lost {
			state = "uncertain"
		}
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"state":"`+state+`"`) {
			t.Fatalf("write outcome lost=%v: %d %s", lost, response.Code, response.Body.String())
		}
	}
	loseResponse.Store(false)

	// A confident runtime callback without an effect must produce failure.
	request.RequestID = uuid.NewString()
	response = performConversationRequest(t, router, http.MethodPost, "/capabilities/invocations", appToken, request)
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &accepted) != nil {
		t.Fatalf("second admit: %s", response.Body.String())
	}
	invocationID = "invocation_" + accepted.RunID
	runtimeID = "sdk-empty-" + uuid.NewString()
	if _, err := database.ActivateAIInvocationRuntime(t.Context(), invocationID, "vercel-workflow", runtimeID); err != nil {
		t.Fatal(err)
	}
	complete(invocationID, runtimeID)
	response = performConversationRequest(t, router, http.MethodGet, "/capabilities/invocations/"+request.RequestID, appToken, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"state":"failed"`) {
		t.Fatalf("false success: %d %s", response.Code, response.Body.String())
	}
	testAIInvocationSDKAccount(t, database, service, record.UserID, targetID, secret)
	testConversationalSDKCrossApp(t, database, service, record.UserID, targetID, secret)
}

// SDK recovery publishes the already observed provider result; a workflow status
// never substitutes for provider evidence and no backend invocation is repeated.
func testSDKRuntimeRecovery(t *testing.T, database *db.Database, service *SpacesService, config AgentRuntimeConfig, user, run, runtime string) {
	t.Helper()
	var observed atomic.Int32
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs/"+runtime+"/status" {
			t.Error("SDK recovery tried a new execution")
			http.Error(w, "unexpected route", 500)
			return
		}
		observed.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "completed"})
	}))
	defer worker.Close()
	original := config
	defer service.SetAgentRuntime(original)
	config.URL = worker.URL
	service.SetAgentRuntime(config)
	if err := database.TestingSpaceTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE ai_invocations SET runtime_heartbeat_at=NOW()-INTERVAL '10 minutes',runtime_observed_at=NOW()-INTERVAL '10 minutes' WHERE id=$1`, run)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProcessAgentRuntimeDeliveries(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	if observed.Load() != 1 {
		t.Fatalf("SDK runtime was not observed: %d", observed.Load())
	}
	record, err := database.AIInvocationByID(t.Context(), user, run)
	if err != nil || record.State != "completed" {
		t.Fatalf("SDK evidence was not reconciled: %#v %v", record, err)
	}
}
