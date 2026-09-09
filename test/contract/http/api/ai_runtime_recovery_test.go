package api

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestQuickAIRuntimeRecoveryUsesPinnedWorkerAndCommittedState(t *testing.T) {
	database := openPresenceTestDatabase(t)
	owner, err := database.CreateUser("Runtime recovery", uniqueTestEmail("runtime-recovery"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	id, runtime := "invocation_"+uuid.NewString(), "pinned-runtime-"+uuid.NewString()
	_, _, err = database.CreateAIInvocationRecord(t.Context(), db.AIInvocationRecord{ID: id, UserID: owner.ID, Mode: "quick", SurfaceID: "settings", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{"prompt":"hello"}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateAIInvocationRuntime(t.Context(), id, "vercel-workflow", runtime); err != nil {
		t.Fatal(err)
	}
	secret := []byte(strings.Repeat("s", 32))
	var responses atomic.Int32
	var starts atomic.Int32
	var replacementCalls atomic.Int32
	var mode atomic.Value
	mode.Store("unavailable")
	var callbackDuringStatus atomic.Bool
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs/"+runtime+"/status" {
			starts.Add(1)
			http.Error(w, "unexpected runtime start", 500)
			return
		}
		if r.Header.Get("X-Misty-Agent-Signature") != TestingAgentRuntimeSignature(secret, r.Method, r.URL.Path, r.Header.Get("X-Misty-Agent-Timestamp"), []byte(`{}`)) {
			t.Error("unsigned runtime observation")
		}
		responses.Add(1)
		if mode.Load() == "unavailable" {
			http.Error(w, "temporary observation failure", 503)
			return
		}
		if callbackDuringStatus.Swap(false) {
			if _, err := database.CommitAIInvocationEvent(t.Context(), owner.ID, id, "late-committed-result", "assistant.message", json.RawMessage(`{"type":"assistant.message","text":"Committed work"}`), ""); err != nil {
				t.Error(err)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": mode.Load().(string)})
	}))
	defer original.Close()
	replacement := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		replacementCalls.Add(1)
		http.Error(w, "wrong worker", 500)
	}))
	defer replacement.Close()
	if _, err := database.BindAgentRuntime(t.Context(), id, original.URL, "https://api.test"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MISTY_AGENT_RUNTIME_URL", replacement.URL)
	t.Setenv("MISTY_AGENT_RUNTIME_INTERNAL_API_URL", "https://replacement-api.test")
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
	mutate := func(query string, args ...any) {
		t.Helper()
		if err := database.TestingSpaceTx(t.Context(), func(tx *sql.Tx) error { _, err := tx.ExecContext(t.Context(), query, args...); return err }); err != nil {
			t.Fatal(err)
		}
	}
	stale := func() {
		mutate(`UPDATE ai_invocations SET runtime_heartbeat_at=NOW()-INTERVAL '10 minutes',runtime_observed_at=NOW()-INTERVAL '10 minutes' WHERE id=$1`, id)
	}
	state := func() string {
		t.Helper()
		record, err := database.AIInvocationByID(t.Context(), owner.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		if record.RuntimeRunID != runtime {
			t.Fatal("runtime identity replaced")
		}
		return record.State
	}
	stale()
	for attempt := 0; attempt < 2; attempt++ {
		n, err := database.ReconcileStaleAIInvocations(t.Context(), time.Now().Add(-5*time.Minute), 20)
		if err != nil || n != 1-attempt {
			t.Fatalf("duplicate status admission: %d %v", n, err)
		}
	}
	if n, err := service.ProcessAgentRuntimeDeliveries(t.Context(), 2); err != nil || n != 0 {
		t.Fatalf("failed observation acknowledged: %d %v", n, err)
	}
	if state() != "running" || responses.Load() != 1 {
		t.Fatal("observation failure stopped or restarted run")
	}
	mode.Store("running")
	mutate(`UPDATE agent_runtime_deliveries SET available_at=NOW() WHERE run_id=$1 AND operation='runtime.reconcile'`, id)
	if n, err := service.ProcessAgentRuntimeDeliveries(t.Context(), 2); err != nil || n != 1 {
		t.Fatalf("observation retry: %d %v", n, err)
	}
	if state() != "running" {
		t.Fatal("running observation changed state")
	}
	// A live workflow can be waiting durably. Checking it must preserve that wait.
	mutate(`UPDATE ai_invocations SET state='awaiting_device' WHERE id=$1`, id)
	stale()
	if _, err := service.ProcessAgentRuntimeDeliveries(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	if state() != "awaiting_device" {
		t.Fatal("status polling resumed a durable wait")
	}
	// A callback committed after observation began outranks its stale terminal reply.
	mode.Store("completed")
	callbackDuringStatus.Store(true)
	stale()
	if _, err := service.ProcessAgentRuntimeDeliveries(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	if state() != "awaiting_device" {
		t.Fatal("stale terminal observation erased a newer callback")
	}
	stale()
	if _, err := service.ProcessAgentRuntimeDeliveries(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	if state() != "failed" {
		t.Fatal("workflow exit was mistaken for successful completion")
	}
	var messages, failures int
	if err := database.TestingSpaceTx(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FILTER(WHERE event_type='assistant.message'),COUNT(*) FILTER(WHERE event_type='invocation.failed') FROM ai_invocation_events WHERE invocation_id=$1`, id).Scan(&messages, &failures)
	}); err != nil || messages != 1 || failures != 1 {
		t.Fatalf("lost committed history: messages=%d failures=%d %v", messages, failures, err)
	}
	if n, err := database.ReconcileStaleAIInvocations(t.Context(), time.Now(), 20); err != nil || n != 0 {
		t.Fatalf("terminal run requeued: %d %v", n, err)
	}
	if starts.Load() != 0 || replacementCalls.Load() != 0 {
		t.Fatalf("recovery switched or restarted worker: starts=%d replacement=%d", starts.Load(), replacementCalls.Load())
	}
}
