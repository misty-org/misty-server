package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testAIInvocationSDKAccount(t *testing.T, database *db.Database, service *SpacesService, user, targetID string, secret []byte) {
	t.Helper()
	id := "invocation_" + uuid.NewString()
	runtime := "sdk-quick-" + uuid.NewString()
	record, _, err := database.CreateAIInvocationRecord(t.Context(), db.AIInvocationRecord{ID: id, UserID: user, SurfaceID: "settings", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{"mode":"quick","surface_id":"settings","trigger":"message","prompt":"List my habits and record that I walked","context":[],"timezone":"UTC","idempotency_key":"quick-sdk"}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateAIInvocationRuntime(t.Context(), id, "vercel-workflow", runtime); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var loseResponse atomic.Bool
	service.TestingSetSDKBackendClientFactory(func(endpoint, bearer string) (*http.Client, error) {
		return &http.Client{Transport: sdkFixtureTransport(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			if loseResponse.Load() {
				return nil, io.ErrUnexpectedEOF
			}
			var body struct {
				Execution cap.Execution `json:"execution"`
				Target    cap.Target    `json:"target"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Execution.RunID != db.SDKPublicRunID(record.ID) || body.Target.ID != targetID || body.Target.SpaceID != "" || body.Execution.EffectID != r.Header.Get("Idempotency-Key") {
				t.Error("quick request lost its account target or run identity")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"status":"success","result":["walked"],"partial":false,"evidence":[]}`))}, nil
		})}, nil
	})
	now := time.Now().UTC()
	token, err := TestingSignMCPAccessToken(secret, user, id, runtime, uuid.NewString(), "", now, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.MistyMCP())
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "sdk-quick-proof", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token, base: http.DefaultTransport}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	catalog, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var read, write string
	for _, tool := range catalog.Tools {
		if strings.Contains(tool.Description, "Capability: habits.list v1.") {
			read = tool.Name
		}
		if strings.Contains(tool.Description, "Capability: habits.record v1.") {
			write = tool.Name
		}
	}
	if read == "" || write == "" {
		t.Fatalf("quick SDK catalog missing: read=%q write=%q", read, write)
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: read, Arguments: map[string]any{}, Meta: mcp.Meta{"misty/call_id": "quick-read"}})
	if err != nil || result.IsError || calls.Load() != 1 {
		t.Fatalf("quick read: %#v %v calls=%d", result, err, calls.Load())
	}
	params := &mcp.CallToolParams{Name: write, Arguments: map[string]any{}, Meta: mcp.Meta{"misty/call_id": "quick-write", "misty/approval_hook_token": "quick-write-hook"}}
	pending, err := session.CallTool(t.Context(), params)
	if err != nil || pending.IsError || pending.Meta["misty/approval"] == nil || calls.Load() != 1 {
		t.Fatalf("quick write bypassed approval: %#v %v", pending, err)
	}
	var approval struct {
		ID string `json:"id"`
	}
	raw, _ := json.Marshal(pending.Meta["misty/approval"])
	if json.Unmarshal(raw, &approval) != nil || approval.ID == "" {
		t.Fatalf("invalid approval: %s", raw)
	}
	testSDKApprovalReview(t, database, user, approval.ID, targetID, "", json.RawMessage(`{}`), token)
	if err := database.DecideSDKToolApproval(t.Context(), user, id, approval.ID, true); err != nil {
		t.Fatal(err)
	}
	result, err = session.CallTool(t.Context(), params)
	if err != nil || result.IsError || calls.Load() != 2 {
		t.Fatalf("quick approved write: %#v %v calls=%d", result, err, calls.Load())
	}
	result, err = session.CallTool(t.Context(), params)
	if err != nil || result.IsError || calls.Load() != 2 {
		t.Fatalf("quick write replay: %#v %v calls=%d", result, err, calls.Load())
	}
	loseResponse.Store(true)
	params.Meta["misty/call_id"] = "quick-lost-write"
	params.Meta["misty/approval_hook_token"] = "quick-lost-write-hook"
	pending, err = session.CallTool(t.Context(), params)
	if err != nil || pending.IsError || pending.Meta["misty/approval"] == nil {
		t.Fatalf("second wait: %#v %v", pending, err)
	}
	raw, _ = json.Marshal(pending.Meta["misty/approval"])
	if json.Unmarshal(raw, &approval) != nil {
		t.Fatal("invalid second approval")
	}
	if err := database.DecideSDKToolApproval(t.Context(), user, id, approval.ID, true); err != nil {
		t.Fatal(err)
	}
	result, err = session.CallTool(t.Context(), params)
	var uncertain struct {
		Status   string `json:"status"`
		EffectID string `json:"effectId"`
	}
	if result != nil {
		raw, _ = json.Marshal(result.StructuredContent)
		_ = json.Unmarshal(raw, &uncertain)
	}
	if err != nil || result.IsError || uncertain.Status != "uncertain" || !cap.ValidID(uncertain.EffectID) || calls.Load() != 3 {
		t.Fatalf("lost quick write: %#v %v calls=%d", result, err, calls.Load())
	}
	params.Meta["misty/call_id"] = "quick-replacement"
	result, err = session.CallTool(t.Context(), params)
	raw, _ = json.Marshal(result)
	if err != nil || result.IsError || result.Meta["misty/approval"] != nil || !strings.Contains(string(raw), uncertain.EffectID) || calls.Load() != 3 {
		t.Fatalf("replanner replaced uncertain effect: %s %v calls=%d", raw, err, calls.Load())
	}
	if unconfirmed, err := database.AgentRunHasUnconfirmedEffects(t.Context(), user, id); err != nil || !unconfirmed {
		t.Fatalf("quick completion missed uncertain effect: %v %v", unconfirmed, err)
	}

}
