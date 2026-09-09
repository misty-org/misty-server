package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAgentModelTurnBudgetRuntimeEndpoint(t *testing.T) {
	database := openPresenceTestDatabase(t)
	user, err := database.CreateUser("Model budget", uniqueTestEmail("model-budget"), "password123")
	if err != nil {
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
	run, _, err := database.CreateAIInvocationRecord(t.Context(), db.AIInvocationRecord{ID: "invocation_" + uuid.NewString(), UserID: user.ID, SurfaceID: "settings", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	runtime := uuid.NewString()
	if _, err := database.ActivateAIInvocationRuntime(t.Context(), run.ID, "vercel-workflow", runtime); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Post("/runtime/{runID}/events", service.AgentRuntimeEvent())
	router.Post("/runtime/{runID}/budget", service.AgentRuntimeExecutionBudget())
	budgetRequest := func(begin bool, identity string) *httptest.ResponseRecorder {
		t.Helper()
		path := "/runtime/" + run.ID + "/budget"
		body, _ := json.Marshal(map[string]any{"runtime_run_id": identity, "begin": begin})
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "budget-read")
		r.Header.Set("X-Misty-Agent-Timestamp", stamp)
		r.Header.Set("X-Misty-Agent-Signature", TestingAgentRuntimeSignature(secret, http.MethodPost, path, stamp, body))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if response := budgetRequest(false, runtime); response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"active":false`) {
		t.Fatalf("dispatch started execution clock: %d %s", response.Code, response.Body.String())
	}
	send := func(node, identity string) *httptest.ResponseRecorder {
		t.Helper()
		path := "/runtime/" + run.ID + "/events"
		body, _ := json.Marshal(map[string]any{"runtime_run_id": identity, "node_id": node, "state": "running", "phase": "thinking", "output": map[string]any{}})
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", node+":running")
		r.Header.Set("X-Misty-Agent-Timestamp", stamp)
		r.Header.Set("X-Misty-Agent-Signature", TestingAgentRuntimeSignature(secret, http.MethodPost, path, stamp, body))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	for i := 1; i <= 20; i++ {
		response := send(fmt.Sprintf("model:%d", i), runtime)
		if response.Code != 200 {
			t.Fatalf("admit model %d: %d %s", i, response.Code, response.Body.String())
		}
	}
	if response := send("model:20", runtime); response.Code != 200 {
		t.Fatalf("lost response replay: %d %s", response.Code, response.Body.String())
	}
	if response := send("model:21", runtime); response.Code != 422 || !strings.Contains(response.Body.String(), "agent_model_turn_limit") {
		t.Fatalf("budget not enforced at runtime: %d %s", response.Code, response.Body.String())
	}
	if response := send("model:20", "different-runtime"); response.Code != 403 {
		t.Fatalf("runtime substitution: %d %s", response.Code, response.Body.String())
	}
	if response := budgetRequest(false, runtime); response.Code != 200 || !strings.Contains(response.Body.String(), `"active":true`) {
		t.Fatalf("model callback did not start the clock: %d %s", response.Code, response.Body.String())
	}
	if _, err := database.Conn.Exec(`UPDATE ai_invocations SET execution_consumed_ms=execution_limit_ms,execution_active_at=NULL WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if response := budgetRequest(true, runtime); response.Code != 422 || !strings.Contains(response.Body.String(), "agent_execution_time_limit") {
		t.Fatalf("budget endpoint admitted exhausted execution: %d %s", response.Code, response.Body.String())
	}
	if response := send("model:20", runtime); response.Code != 422 || !strings.Contains(response.Body.String(), "agent_execution_time_limit") {
		t.Fatalf("model checkpoint ignored exhausted time: %d %s", response.Code, response.Body.String())
	}
	if response := budgetRequest(false, "different-runtime"); response.Code != 403 {
		t.Fatalf("budget runtime substitution: %d", response.Code)
	}
}
