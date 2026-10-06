package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/test/testkit"
)

type runtimeBillingFixture struct {
	receipts map[string]billingadapter.Request
}

func (*runtimeBillingFixture) Enabled() bool { return true }
func (f *runtimeBillingFixture) Do(_ context.Context, action string, r billingadapter.Request) (billingadapter.Decision, error) {
	if previous, ok := f.receipts[r.Key]; ok && !reflect.DeepEqual(previous, r) {
		return billingadapter.Decision{}, billingadapter.ErrConflict
	}
	f.receipts[r.Key] = r
	id := r.ReservationID
	if action == "reserve" {
		id = "reserved:" + r.Key
	}
	return billingadapter.Decision{Allowed: true, ReservationID: id}, nil
}

func TestRuntimeCanceledCompletionSettlesOriginalHolds(t *testing.T) {
	if os.Getenv("TEST_DB_NAME") != "misty_agent_phases34_test" {
		t.Skip("requires isolated phase3/4 database")
	}
	database := testkit.OpenDatabase(t)
	// The testkit owns one lock connection. Accounting must work with only one
	// remaining connection, including its durable journal and delivery receipt.
	database.Conn.SetMaxOpenConns(2)
	ctx := t.Context()
	owner, err := database.CreateUser("Billing validation", "runtime-billing@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{ID: "invocation_billing-cancel", UserID: owner.ID, SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "running", IdempotencyKey: "billing-cancel", RequestPayload: json.RawMessage(`{"prompt":"billing test"}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	runtimeID := "fixture-runtime"
	if _, err = database.ActivateAIInvocationRuntime(ctx, record.ID, "vercel-workflow", runtimeID); err != nil {
		t.Fatal(err)
	}
	adapter := &runtimeBillingFixture{receipts: map[string]billingadapter.Request{}}
	meter := agent.BillingMeter{Service: &billingadapter.Service{Adapter: adapter, Store: db.BillingOutbox{Database: database}}}
	key := "agent-runtime:" + record.ID + ":model:model:1"
	if _, err = meter.Reserve(owner.ID, key, "assistant_ai", "ai-gateway", "openai/gpt-6-astra", 32000, agent.MaxModelOutputTokens); err != nil {
		t.Fatal(err)
	}
	key2 := "agent-runtime:" + record.ID + ":model:model:2"
	if _, err = meter.Reserve(owner.ID, key2, "assistant_ai", "ai-gateway", "openai/gpt-6-astra", 32000, agent.MaxModelOutputTokens); err != nil {
		t.Fatal(err)
	}
	if err = database.CancelMistyInvocationChildren(ctx, owner.ID, record.ID); err != nil {
		t.Fatal(err)
	}
	secret := []byte(strings.Repeat("s", 32))
	service := &SpacesService{database: database, usageMeter: meter, agentRuntime: AgentRuntimeConfig{URL: "https://runtime.example.test", secret: secret}}
	router := chi.NewRouter()
	router.Post("/internal/agent-runtime/runs/{runID}/complete", service.AgentRuntimeComplete())
	router.Post("/internal/agent-runtime/runs/{runID}/events", service.AgentRuntimeEvent())
	invoke := func(runtime, status string, usage any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"runtime_run_id": runtime, "status": status, "usage": usage})
		path := "/internal/agent-runtime/runs/" + record.ID + "/complete"
		r := httptest.NewRequest("POST", path, bytes.NewReader(body))
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		r.Header.Set("X-Misty-Agent-Timestamp", stamp)
		r.Header.Set("X-Misty-Agent-Signature", signAgentRuntimeRequest(secret, "POST", path, stamp, body))
		r.Header.Set("Idempotency-Key", "fixture-complete")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	// Stop races with the measured per-step callback. It must settle that hold
	// without allowing any further model/tool work or resurrecting the invocation.
	for range 2 {
		body, _ := json.Marshal(map[string]any{"runtime_run_id": runtimeID, "node_id": "model:1", "state": "completed", "phase": "working", "output": map[string]any{"usage": map[string]any{"inputTokens": 123, "outputTokens": 45}}})
		path := "/internal/agent-runtime/runs/" + record.ID + "/events"
		r := httptest.NewRequest("POST", path, bytes.NewReader(body))
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		r.Header.Set("X-Misty-Agent-Timestamp", stamp)
		r.Header.Set("X-Misty-Agent-Signature", signAgentRuntimeRequest(secret, "POST", path, stamp, body))
		r.Header.Set("Idempotency-Key", "fixture-model-complete")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("late measured checkpoint: %d %s", w.Code, w.Body)
		}
	}
	measured, ok := adapter.receipts[key+":settle"]
	if !ok || measured.ReservationID != "reserved:"+key || measured.Usage.Units["input_tokens"] != 123 {
		t.Fatalf("per-step usage not settled: %+v", measured)
	}
	usage := map[string]any{"inputTokens": 246, "outputTokens": 90}
	if w := invoke("wrong-runtime", "failed", usage); w.Code == http.StatusOK {
		t.Fatal("foreign runtime could settle")
	}
	if w := invoke(runtimeID, "invalid", usage); w.Code != http.StatusBadRequest {
		t.Fatal("invalid terminal status accepted", w.Code)
	}
	for range 2 {
		if w := invoke(runtimeID, "failed", usage); w.Code != http.StatusOK {
			t.Fatalf("late completion: %d %s", w.Code, w.Body)
		}
	}
	settled, ok := adapter.receipts["agent-runtime:"+record.ID+":settle"]
	if !ok || settled.AccountID != owner.ID || settled.Usage.Units["input_tokens"] != 123 || len(settled.ReservationIDs) != 1 || settled.ReservationIDs[0] != "reserved:"+key2 {
		t.Fatalf("canceled work lost measured settlement: %+v", settled)
	}
	var n int
	if err = database.Conn.QueryRow(`SELECT count(*) FROM billing_adapter_outbox WHERE payload->>'account_id'=$1`, owner.ID).Scan(&n); err != nil || n != 2 {
		t.Fatal("completion replay duplicated durable receipt", n, err)
	}
	persisted, err := database.AIInvocationByID(ctx, owner.ID, record.ID)
	if err != nil || persisted.State != "canceled" {
		t.Fatal("completion resurrected canceled work", err)
	}
}

func TestRuntimeUsageRequiresRawMeasuredCounters(t *testing.T) {
	raw := json.RawMessage(`{"usage":{"inputTokens":123,"outputTokens":45,"inputTokenDetails":{"cacheReadTokens":12},"outputTokenDetails":{"reasoningTokens":7}}}`)
	usage := agentRuntimeModelUsage(raw)
	if usage.Estimated || usage.InputTokens != 123 || usage.CachedInputTokens != 12 || usage.OutputTokens != 45 || usage.ReasoningTokens != 7 {
		t.Fatal(usage)
	}
	aggregate := agentRuntimeModelUsage(json.RawMessage(`{"inputTokens":123,"outputTokens":45,"inputTokenDetails":{},"outputTokenDetails":{}}`))
	if aggregate.Estimated || !aggregate.CachedInputTokensMissing || !aggregate.ReasoningTokensMissing {
		t.Fatal("missing aggregate details treated as measured zero", aggregate)
	}
	explicit := agentRuntimeModelUsage(json.RawMessage(`{"inputTokens":123,"outputTokens":45,"inputTokenDetails":{"cacheReadTokens":0},"outputTokenDetails":{"reasoningTokens":0}}`))
	if explicit.CachedInputTokensMissing || explicit.ReasoningTokensMissing {
		t.Fatal("explicit zero details treated as unavailable", explicit)
	}
	for _, bad := range []json.RawMessage{sanitizeAgentLifecycleJSON(raw), json.RawMessage(`{"inputTokens":123}`), json.RawMessage(`{"inputTokens":-1,"outputTokens":20}`), json.RawMessage(`{"inputTokens":1,"outputTokens":20,"inputTokenDetails":{"cacheReadTokens":2}}`)} {
		if !agentRuntimeModelUsage(bad).Estimated {
			t.Fatalf("untrusted counters accepted: %s", bad)
		}
	}
}
