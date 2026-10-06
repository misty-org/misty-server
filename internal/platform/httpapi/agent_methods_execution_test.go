package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"github.com/kannachi323/misty/server/test/testkit"
)

// Admission, ownership, persistence, scheduler and completion projection are real.
// Only the remote model/workflow engine is a deterministic fixture: this is not a
// live model, browser or integration acceptance test.
func TestAgentMethodHTTPManualAndScheduledExecution(t *testing.T) {
	if os.Getenv("TEST_DB_NAME") != "misty_agent_phases34_test" {
		t.Skip("requires isolated phase3/4 database")
	}
	database := testkit.OpenDatabase(t)
	ctx := t.Context()
	t.Setenv("MISTY_AGENT_MODEL", "openai/gpt-6-astra")
	t.Setenv("AI_GATEWAY_API_KEY", "fixture-not-a-real-key")
	t.Setenv("AI_GATEWAY_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("MISTY_AI_MODEL_CATALOG_JSON", `[{"id":"openai/gpt-6-astra","name":"GPT-6 Astra","capabilities":["chat","tools","vision","reasoning"]}]`)
	owner, err := database.CreateUser("Workflow execution", "workflow-execution@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Foreign owner", "workflow-foreign@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := database.SavePersonalAgent(ctx, owner.ID, "", db.AgentProfileInput{Name: "Research", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := database.CreatePersonalAgentConversation(ctx, owner.ID, "", agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the testkit advisory-lock connection and one application connection.
	// Mirror baseline application grants from postgres-grant-app-role.sh in this
	// disposable database. New method-table grants remain those from the migration.
	if _, err = database.Conn.Exec(`DO $$ DECLARE tab record; BEGIN FOR tab IN SELECT tablename FROM pg_tables WHERE schemaname='public' AND tablename NOT IN ('agent_methods','agent_method_versions','composio_sessions','agent_app_requests') LOOP EXECUTE format('GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE public.%I TO misty_app',tab.tablename); END LOOP; END $$; GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO misty_app;`); err != nil {
		t.Fatal(err)
	}
	database.Conn.SetMaxOpenConns(2)
	if _, err = database.Conn.Exec(`SET ROLE misty_app`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, e := database.Conn.Exec(`RESET ROLE`); e != nil {
			t.Error(e)
		}
	})
	definition := db.AgentMethodDefinition{Title: "Topic brief", Instructions: "Write a short briefing about the supplied topic.", Target: "cloud", Inputs: []db.AgentMethodInput{{Key: "topic", Label: "Topic", Type: "text", Required: true}}}
	method, err := database.SaveAgentMethod(ctx, owner.ID, db.AgentMethod{AgentID: agent.ID, Kind: "workflow", Enabled: true, Definition: definition}, 0)
	if err != nil {
		t.Fatal(err)
	}
	originalVersion := method.VersionID
	method.Definition.Instructions = "Changed instructions that must not appear in the pinned run."
	if _, err = database.SaveAgentMethod(ctx, owner.ID, method, method.Version); err != nil {
		t.Fatal(err)
	}
	var starts atomic.Int32
	secret := []byte(strings.Repeat("s", 32))
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/v1/runs" || r.Header.Get("X-Misty-Agent-Signature") != signAgentRuntimeRequest(secret, r.Method, r.URL.Path, r.Header.Get("X-Misty-Agent-Timestamp"), raw) {
			http.Error(w, "unexpected or unsigned runtime dispatch", 400)
			return
		}
		var request agentRuntimeStartRequest
		if json.Unmarshal(raw, &request) != nil || r.Header.Get("Idempotency-Key") != request.RunID {
			http.Error(w, "invalid stable dispatch identity", 400)
			return
		}
		starts.Add(1)
		runtimeID := "fixture-" + request.RunID
		if _, err := database.ActivateAIInvocationRuntime(r.Context(), request.RunID, "vercel-workflow", runtimeID); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 200, map[string]string{"runtime_run_id": runtimeID})
	}))
	defer runtime.Close()
	service := NewAIService(database, nil)
	service.SetAgentRuntime(AgentRuntimeConfig{Kind: "vercel-workflow", URL: runtime.URL, InternalAPIURL: "https://api.example.test", secret: secret, client: runtime.Client()})
	spaces := &SpacesService{database: database, aiInvocations: service.invocations}
	router := chi.NewRouter()
	router.Post("/methods/run", service.RunAgentMethod())
	router.Put("/methods/{methodID}/schedule", service.WorkflowSchedule())
	router.Post("/invocations/{invocationID}/cancel", service.CancelInvocation())
	router.Get("/conversations", service.MistyConversations())
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	token := func(user string) string {
		v, e := signer.Mint(user, "fixture-session", "access", time.Now().Add(time.Hour))
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	invoke := func(path, user string, body any) *httptest.ResponseRecorder {
		raw, e := json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		r.AddCookie(&http.Cookie{Name: TestingSessionCookieName, Value: token(user)})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	// A fresh canonical conversation has no legacy runtime state and no turns.
	// It must still load before a workflow can start its separate browser window.
	get := func(path, user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(&http.Cookie{Name: TestingSessionCookieName, Value: token(user)})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	assertEmpty := func(item mistyConversation) {
		t.Helper()
		if item.ID != conversation || item.AgentID != agent.ID || item.Kind != "misty" || item.Messages == nil || len(item.Messages) != 0 {
			t.Fatalf("fresh canonical conversation lost identity or empty messages: %+v", item)
		}
	}
	freshList := get("/conversations", owner.ID)
	var listed struct {
		Conversations []mistyConversation `json:"conversations"`
	}
	if freshList.Code != http.StatusOK || json.Unmarshal(freshList.Body.Bytes(), &listed) != nil {
		t.Fatalf("fresh canonical conversation list: %d %s", freshList.Code, freshList.Body)
	}
	found := false
	for _, item := range listed.Conversations {
		if item.ID == conversation {
			assertEmpty(item)
			found = true
		}
	}
	if !found {
		t.Fatal("fresh canonical conversation omitted from list")
	}
	foreignList := get("/conversations", other.ID)
	var foreign struct {
		Conversations []mistyConversation `json:"conversations"`
	}
	if foreignList.Code != http.StatusOK || json.Unmarshal(foreignList.Body.Bytes(), &foreign) != nil {
		t.Fatalf("foreign owner's list: %d %s", foreignList.Code, foreignList.Body)
	}
	for _, item := range foreign.Conversations {
		if item.ID == conversation {
			t.Fatal("canonical conversation leaked in foreign list")
		}
	}
	input := map[string]any{"version_id": originalVersion, "inputs": map[string]any{"topic": "Ocean currents"}, "conversation_id": conversation, "idempotency_key": "manual-method-once", "prompt": "Forged prompt"}
	if w := invoke("/methods/run", other.ID, input); w.Code < 400 {
		t.Fatal("foreign workflow admitted", w.Body.String())
	}
	invalid := map[string]any{"version_id": originalVersion, "inputs": map[string]any{"topic": 42}, "conversation_id": conversation, "idempotency_key": "invalid-input"}
	if w := invoke("/methods/run", owner.ID, invalid); w.Code != 400 {
		t.Fatalf("typed input validation %d %s", w.Code, w.Body)
	}
	w := invoke("/methods/run", owner.ID, input)
	if w.Code != http.StatusAccepted {
		t.Fatalf("manual admission %d %s", w.Code, w.Body)
	}
	var admitted struct {
		ID string `json:"invocationId"`
	}
	if json.Unmarshal(w.Body.Bytes(), &admitted) != nil || admitted.ID == "" {
		t.Fatal("no admitted run")
	}
	duplicate := invoke("/methods/run", owner.ID, input)
	var again struct {
		ID string `json:"invocationId"`
	}
	_ = json.Unmarshal(duplicate.Body.Bytes(), &again)
	if duplicate.Code != 202 || again.ID != admitted.ID {
		t.Fatal("manual duplicate changed identity", duplicate.Body.String())
	}
	assertPin := func(id string) *db.AIInvocationRecord {
		t.Helper()
		record, e := database.AIInvocationByID(ctx, owner.ID, id)
		if e != nil {
			t.Fatal(e)
		}
		var body aiInvocationInput
		if json.Unmarshal(record.RequestPayload, &body) != nil || body.MethodVersionID != originalVersion || body.AgentID != agent.ID || body.MethodInputs["topic"] != "Ocean currents" || !strings.Contains(body.Prompt, definition.Instructions) || strings.Contains(body.Prompt, "Changed instructions") || strings.Contains(body.Prompt, "Forged prompt") {
			t.Fatal("run lost immutable version, typed input or trusted prompt", string(record.RequestPayload))
		}
		return record
	}
	assertPin(admitted.ID)
	// The manual HTTP path queues a durable delivery; dispatch it to the fixture.
	var deliveries int
	if err = database.TestingWithRLSContext(ctx, db.TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_runtime_deliveries WHERE run_id=$1 AND operation='invocation.start'`, admitted.ID).Scan(&deliveries)
	}); err != nil || deliveries != 1 {
		t.Fatal("manual dispatch not durable once", deliveries, err)
	}
	if _, err = service.agentRuntime.Start(ctx, admitted.ID); err != nil {
		t.Fatal(err)
	}
	finish := func(id string) {
		t.Helper()
		record := assertPin(id)
		var body aiInvocationInput
		_ = json.Unmarshal(record.RequestPayload, &body)
		if _, e := service.invocations.restoreDurable(ctx, *record); e != nil {
			t.Fatal(e)
		}
		if e := spaces.finishAIInvocationRuntimeAnswer(owner.ID, id, body, "Ocean currents distribute heat around the planet.", nil, body.Prompt); e != nil {
			t.Fatal(e)
		}
		events, state, e := database.AIInvocationEvents(ctx, owner.ID, id, 0)
		messages := 0
		for _, event := range events {
			if event.EventType == "assistant.message" {
				messages++
			}
		}
		if e != nil || state != "completed" || messages != 1 {
			t.Fatal("completion not persisted once", state, messages, e)
		}
	}
	finish(admitted.ID)

	// Cancellation is an owned, atomic state transition. A completed result cannot
	// be overwritten, and concurrent Stop clicks must persist exactly one receipt.
	completedStop := invoke("/invocations/"+admitted.ID+"/cancel", owner.ID, nil)
	if completedStop.Code != http.StatusAccepted {
		t.Fatalf("completed run stop: %d %s", completedStop.Code, completedStop.Body)
	}
	completedEvents, completedState, completedErr := database.AIInvocationEvents(ctx, owner.ID, admitted.ID, 0)
	if completedErr != nil || completedState != "completed" {
		t.Fatal("stop changed completed run", completedState, completedErr)
	}
	for _, event := range completedEvents {
		if event.EventType == "invocation.canceled" {
			t.Fatal("stop added a cancellation to completed work")
		}
	}
	cancelConversation, err := database.CreatePersonalAgentConversation(ctx, owner.ID, "", agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	cancelPayload, err := json.Marshal(aiInvocationInput{AgentID: agent.ID, ExecutionMode: "user", Prompt: "Wait for an explicit stop", ConversationID: cancelConversation})
	if err != nil {
		t.Fatal(err)
	}
	cancelRecord, _, err := database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{
		ID: "invocation_cancel-method-fixture", UserID: owner.ID, ConversationID: cancelConversation,
		SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "running",
		IdempotencyKey: "cancel-method-fixture", RequestPayload: cancelPayload, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelPath := "/invocations/" + cancelRecord.ID + "/cancel"
	if foreignStop := invoke(cancelPath, other.ID, nil); foreignStop.Code != http.StatusNotFound {
		t.Fatalf("foreign stop: %d %s", foreignStop.Code, foreignStop.Body)
	}
	unchanged, err := database.AIInvocationByID(ctx, owner.ID, cancelRecord.ID)
	if err != nil || unchanged.State != "running" {
		t.Fatal("foreign stop affected owned run", err)
	}
	stops := make(chan *httptest.ResponseRecorder, 6)
	for range 6 {
		go func() { stops <- invoke(cancelPath, owner.ID, nil) }()
	}
	for range 6 {
		stop := <-stops
		if stop.Code != http.StatusAccepted {
			t.Errorf("concurrent stop: %d %s", stop.Code, stop.Body)
		}
	}
	if duplicateStop := invoke(cancelPath, owner.ID, nil); duplicateStop.Code != http.StatusAccepted {
		t.Fatalf("duplicate stop: %d %s", duplicateStop.Code, duplicateStop.Body)
	}
	canceledEvents, canceledState, canceledErr := database.AIInvocationEvents(ctx, owner.ID, cancelRecord.ID, 0)
	cancellationReceipts := 0
	for _, event := range canceledEvents {
		if event.EventType == "invocation.canceled" {
			cancellationReceipts++
		}
	}
	if canceledErr != nil || canceledState != "canceled" || cancellationReceipts != 1 {
		t.Fatal("cancellation was not persisted exactly once", canceledState, cancellationReceipts, canceledErr)
	}
	// A successful original run can seed a method; another owner's source cannot.
	derived, err := database.SaveAgentMethod(ctx, owner.ID, db.AgentMethod{AgentID: agent.ID, Kind: "template", Enabled: true, Definition: definition, SourceInvocationID: admitted.ID}, 0)
	if err != nil || derived.SourceInvocationID != admitted.ID {
		t.Fatal("completed provenance", err)
	}
	if _, err = database.SaveAgentMethod(ctx, other.ID, db.AgentMethod{AgentID: agent.ID, Kind: "template", Enabled: true, Definition: definition, SourceInvocationID: admitted.ID}, 0); err == nil {
		t.Fatal("foreign source accepted")
	}
	// A schedule runs the workflow's latest version, never a forged prompt.
	scheduleBody := map[string]any{"inputs": map[string]any{"topic": "Ocean currents"}, "prompt": "Forged schedule instructions", "timezone": "UTC", "rules": []any{map[string]any{"frequency": "daily", "times": []string{"09:00"}}}}
	raw, _ := json.Marshal(scheduleBody)
	put := httptest.NewRequest(http.MethodPut, "/methods/"+method.ID+"/schedule", bytes.NewReader(raw))
	put.AddCookie(&http.Cookie{Name: TestingSessionCookieName, Value: token(owner.ID)})
	w = httptest.NewRecorder()
	router.ServeHTTP(w, put)
	if w.Code != http.StatusOK {
		t.Fatalf("schedule creation %d %s", w.Code, w.Body)
	}
	var created struct {
		Schedule db.WorkflowSchedule `json:"schedule"`
	}
	if json.Unmarshal(w.Body.Bytes(), &created) != nil || created.Schedule.MethodID != method.ID {
		t.Fatal("schedule not attached to its workflow")
	}
	now := time.Now().UTC()
	if err = database.TestingWithRLSContext(ctx, db.TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `UPDATE workflow_schedules SET next_run_at=$2 WHERE id=$1`, created.Schedule.ID, now)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	n, err := service.ProcessDueWorkflowSchedules(ctx, now.Add(time.Second), 10)
	if err != nil || n != 1 {
		t.Fatal("scheduled admission", n, err)
	}
	running, err := database.WorkflowScheduleByMethod(ctx, owner.ID, method.ID)
	if err != nil || running.LastInvocationID == "" || running.State != "running" {
		t.Fatal("schedule not bound", err)
	}
	latest, err := database.AgentMethodByID(ctx, owner.ID, method.ID)
	if err != nil {
		t.Fatal(err)
	}
	scheduledRecord, err := database.AIInvocationByID(ctx, owner.ID, running.LastInvocationID)
	if err != nil {
		t.Fatal(err)
	}
	var scheduled aiInvocationInput
	if json.Unmarshal(scheduledRecord.RequestPayload, &scheduled) != nil || scheduled.MethodVersionID != latest.VersionID || !strings.Contains(scheduled.Prompt, "Changed instructions") || strings.Contains(scheduled.Prompt, "Forged") || scheduled.MethodInputs["topic"] != "Ocean currents" {
		t.Fatal("scheduled run did not use the latest version and saved inputs", string(scheduledRecord.RequestPayload))
	}
	if err = service.startWorkflowScheduleRun(ctx, *running, now.Add(time.Second)); err != nil {
		t.Fatal("occurrence retry", err)
	}
	repeated, err := database.WorkflowScheduleByMethod(ctx, owner.ID, method.ID)
	if err != nil || repeated.LastInvocationID != running.LastInvocationID || starts.Load() != 2 {
		t.Fatal("same occurrence dispatched twice", starts.Load(), err)
	}
	if _, e := service.invocations.restoreDurable(ctx, *scheduledRecord); e != nil {
		t.Fatal(e)
	}
	if e := spaces.finishAIInvocationRuntimeAnswer(owner.ID, scheduledRecord.ID, scheduled, "Ocean currents distribute heat around the planet.", nil, scheduled.Prompt); e != nil {
		t.Fatal(e)
	}
	if err = spaces.completeWorkflowScheduleInvocation(ctx, scheduledRecord, nil); err != nil {
		t.Fatal(err)
	}
	settled, err := database.WorkflowScheduleByMethod(ctx, owner.ID, method.ID)
	if err != nil || settled.State != "idle" || settled.RunCount != 1 || settled.LastError != "" || settled.NextRunAt == nil || !settled.NextRunAt.After(now) {
		t.Fatal("schedule did not settle", settled, err)
	}
}
