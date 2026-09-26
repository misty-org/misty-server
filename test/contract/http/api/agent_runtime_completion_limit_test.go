package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	agent "github.com/kannachi323/misty/server/internal/agents"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type completionLimitMeter struct {
	agent.UsageMeter
	err error
}

func (m completionLimitMeter) Reserve(string, string, string, string, string, int64, int64) (*agent.UsageReservation, error) {
	return &agent.UsageReservation{}, nil
}
func (m completionLimitMeter) Settle(*agent.UsageReservation, string, string, string, string, agent.ModelUsage) (agent.UsageSettlement, error) {
	return agent.UsageSettlement{}, m.err
}

func TestRuntimeCompletionLimitTerminatesInvocation(t *testing.T) {
	for _, scenario := range []string{"success", "incomplete", "transient"} {
		t.Run(scenario, func(t *testing.T) {
			database := openPresenceTestDatabase(t)
			owner, err := database.CreateUserWithUsername("Completion test", "limit"+strings.ReplaceAll(uuid.NewString(), "-", "")[:12], uniqueTestEmail("completion-limit"), "password123")
			if err != nil {
				t.Fatal(err)
			}
			id, runtime := "invocation_"+uuid.NewString(), "runtime-"+uuid.NewString()
			_, _, err = database.CreateAIInvocationRecord(t.Context(), db.AIInvocationRecord{ID: id, UserID: owner.ID, Mode: "quick", SurfaceID: "settings", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{"prompt":"Inspect the website"}`), ExpiresAt: time.Now().Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.ActivateAIInvocationRuntime(t.Context(), id, "vercel-workflow", runtime); err != nil {
				t.Fatal(err)
			}
			secret := []byte(strings.Repeat("s", 32))
			t.Setenv("MISTY_AGENT_RUNTIME_URL", "https://worker.test")
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
			NewAIService(database, nil).AttachSpacesRuntime(service)
			var meterErr error = agent.HostedAILimitReachedError{Scope: "personal"}
			status := scenario
			if scenario == "transient" {
				meterErr = errors.New("temporary database failure")
				status = "success"
			}
			service.SetUsageMeter(completionLimitMeter{err: meterErr})
			router := chi.NewRouter()
			router.Post("/internal/agent-runtime/runs/{runID}/complete", service.AgentRuntimeComplete())
			invoke := func() *httptest.ResponseRecorder {
				body, _ := json.Marshal(map[string]any{"runtime_run_id": runtime, "status": status, "text": "The website requires sign-in.", "usage": map[string]int{"inputTokens": 100, "outputTokens": 20}})
				path := "/internal/agent-runtime/runs/" + id + "/complete"
				r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
				timestamp := strconv.FormatInt(time.Now().Unix(), 10)
				r.Header.Set("X-Misty-Agent-Timestamp", timestamp)
				r.Header.Set("Idempotency-Key", id+":complete")
				r.Header.Set("X-Misty-Agent-Signature", TestingAgentRuntimeSignature(secret, http.MethodPost, path, timestamp, body))
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				return w
			}
			response := invoke()
			record, err := database.AIInvocationByID(t.Context(), owner.ID, id)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "transient" {
				if response.Code < 500 || record.State != "running" {
					t.Fatalf("transient accounting failure was acknowledged: %d %s %s", response.Code, record.State, response.Body)
				}
				return
			}
			if response.Code != 200 || record.State != "failed" {
				t.Fatalf("quota failure left task active: %d %s %s", response.Code, record.State, response.Body)
			}
			if replay := invoke(); replay.Code != 200 {
				t.Fatalf("completion retry failed: %d %s", replay.Code, replay.Body)
			}
			var failed, completed int
			if err := database.Conn.QueryRow(`SELECT COUNT(*) FILTER (WHERE event_type='invocation.failed'), COUNT(*) FILTER (WHERE event_type='invocation.completed') FROM ai_invocation_events WHERE invocation_id=$1`, id).Scan(&failed, &completed); err != nil {
				t.Fatal(err)
			}
			if failed != 1 || completed != 0 {
				t.Fatalf("terminal events: failed=%d completed=%d", failed, completed)
			}
		})
	}
}
