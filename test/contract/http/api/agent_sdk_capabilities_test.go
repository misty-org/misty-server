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
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testConversationalSDKCrossApp(t *testing.T, database *db.Database, service *SpacesService, user, targetID string, secret []byte) {
	t.Helper()
	space, err := database.CreateSpace(t.Context(), user, "SDK cross-app proof")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ConfigureSDKTarget(t.Context(), user, cap.TargetConfiguration{TargetID: targetID, ExpectedRevision: 1, ProviderID: "example.habits/backend", ProviderVersion: 1, SpaceID: space.ID, Label: "Habits for this Space", Capabilities: []string{"habits.list", "habits.record"}, CallerApps: []string{"example.habits"}}); err != nil {
		t.Fatal(err)
	}
	agent, err := database.EnsureAskIdentity(t.Context(), user, serveragent.InitialSelectedModelID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := database.CreateCreatorAgentRun(t.Context(), user, space.ID, agent.ID, db.CreatorAgentRunInput{Instruction: "Record that I walked, then create a Journal summary.", Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := database.ClaimPersonalAgentTaskRunJobs(t.Context(), "sdk-conversation-fixture", 1, time.Minute)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim: %#v %v", jobs, err)
	}
	runtime := "sdk-conversation-" + uuid.NewString()
	if _, err := database.ActivatePersonalAgentTaskRuntime(t.Context(), run.ID, "vercel-workflow", runtime); err != nil {
		t.Fatal(err)
	}
	var writes atomic.Int32
	var loseResponse atomic.Bool
	var effect string
	service.TestingSetSDKBackendClientFactory(func(endpoint, bearer string) (*http.Client, error) {
		if endpoint != "https://habits.example.com/execute" || bearer != "private-provider-token" {
			t.Error("unbound backend connection")
		}
		return &http.Client{Transport: sdkFixtureTransport(func(r *http.Request) (*http.Response, error) {
			var body struct {
				Execution cap.Execution `json:"execution"`
				Target    cap.Target    `json:"target"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Execution.RunID != strings.TrimPrefix(run.ID, "run_") || body.Target.SpaceID != space.ID || body.Target.Revision != 2 || body.Execution.EffectID != r.Header.Get("Idempotency-Key") {
				t.Error("conversational execution lost its pinned identity")
			}
			deadline, bounded := r.Context().Deadline()
			if !bounded || !body.Execution.Deadline.Equal(deadline) || body.Execution.Deadline.After(time.Now().Add(31*time.Second)) {
				t.Error("provider did not receive the bounded execution deadline")
			}
			effect = body.Execution.EffectID
			writes.Add(1)
			if loseResponse.Load() {
				return nil, io.ErrUnexpectedEOF
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"status":"success","result":["walked"],"partial":false,"evidence":[]}`))}, nil
		})}, nil
	})
	now := time.Now().UTC()
	token, err := TestingSignMCPAccessToken(secret, user, run.ID, runtime, uuid.NewString(), "", now, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.MistyMCP())
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "sdk-cross-app-proof", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token, base: http.DefaultTransport}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	catalog, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var sdkWrite string
	var notes bool
	for _, tool := range catalog.Tools {
		if strings.Contains(tool.Description, "Capability: habits.record v1.") {
			sdkWrite = tool.Name
		}
		if tool.Name == "notes.create" {
			notes = true
		}
	}
	if sdkWrite == "" || !notes {
		t.Fatalf("cross-app registry missing provider or Journal: sdk=%q notes=%v tools=%d", sdkWrite, notes, len(catalog.Tools))
	}
	params := &mcp.CallToolParams{Name: sdkWrite, Arguments: map[string]any{}, Meta: mcp.Meta{"misty/call_id": "record-habit", "misty/approval_hook_token": "record-habit-hook"}}
	pending, err := session.CallTool(t.Context(), params)
	if err != nil || pending.IsError || pending.Meta["misty/approval"] == nil || writes.Load() != 0 {
		t.Fatalf("unapproved SDK effect: %#v %v", pending, err)
	}
	raw, _ := json.Marshal(pending.Meta["misty/approval"])
	var approval struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &approval) != nil || approval.ID == "" {
		t.Fatalf("approval identity: %s", raw)
	}
	testSDKApprovalReview(t, database, user, approval.ID, targetID, space.ID, json.RawMessage(`{}`), token)
	if _, err := database.DecideCreatorToolApproval(t.Context(), user, run.ID, approval.ID, true); err != nil {
		t.Fatal(err)
	}
	executed, err := session.CallTool(t.Context(), params)
	if err != nil || executed.IsError || writes.Load() != 1 || !cap.ValidID(effect) {
		t.Fatalf("approved SDK action: %#v %v calls=%d", executed, err, writes.Load())
	}
	replay, err := session.CallTool(t.Context(), params)
	if err != nil || replay.IsError || writes.Load() != 1 {
		t.Fatalf("SDK replay repeated effect: %#v %v calls=%d", replay, err, writes.Load())
	}
	journalParams := &mcp.CallToolParams{Name: "notes.create", Arguments: map[string]any{"title": "Habit summary", "markdown": "Recorded habit: walked."}, Meta: mcp.Meta{"misty/call_id": "journal-summary", "misty/approval_hook_token": "journal-summary-hook"}}
	summary, err := session.CallTool(t.Context(), journalParams)
	if err != nil || summary.IsError {
		t.Fatalf("Journal summary: %#v %v", summary, err)
	}
	// Force clock exhaustion in this disposable fixture. Both provider and built-in
	// confirmed effects must remain replayable while fresh writes are refused.
	if _, err := database.Conn.Exec(`UPDATE space_runs SET execution_consumed_ms=execution_limit_ms,execution_active_at=NULL WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	for _, confirmed := range []*mcp.CallToolParams{params, journalParams} {
		replayed, err := session.CallTool(t.Context(), confirmed)
		if err != nil || replayed.IsError || writes.Load() != 1 {
			t.Fatalf("exhaustion blocked confirmed replay: %#v %v", replayed, err)
		}
	}
	refused, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "notes.create", Arguments: map[string]any{"title": "Budget must prevent this note", "markdown": "Unapproved extra work"}, Meta: mcp.Meta{"misty/call_id": "over-budget", "misty/approval_hook_token": "over-budget-hook"}})
	encodedRefusal, _ := json.Marshal(refused)
	if err != nil || !refused.IsError || !strings.Contains(string(encodedRefusal), "agent_execution_time_limit") {
		t.Fatalf("fresh write escaped time allowance: %s %v", encodedRefusal, err)
	}
	var blockedNotes int
	if err := database.Conn.QueryRow(`SELECT count(*) FROM space_notes WHERE space_id=$1 AND title_projection='Budget must prevent this note'`, space.ID).Scan(&blockedNotes); err != nil || blockedNotes != 0 {
		t.Fatalf("budget refusal still wrote: %d %v", blockedNotes, err)
	}
	// Restore the synthetic clock only in this fixture to continue the separate
	// lost-reply scenario below. There is no application API for budget resets.
	if _, err := database.Conn.Exec(`UPDATE space_runs SET execution_consumed_ms=0,execution_active_at=NULL WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	loseResponse.Store(true)
	var uncertainEffect string
	for _, callID := range []string{"lost-habit-reply", "replanner-replacement"} {
		params.Meta["misty/call_id"] = callID
		params.Meta["misty/approval_hook_token"] = callID + "-hook"
		pending, err = session.CallTool(t.Context(), params)
		if callID == "replanner-replacement" {
			encoded, _ := json.Marshal(pending)
			if err != nil || pending.IsError || pending.Meta["misty/approval"] != nil || !strings.Contains(string(encoded), uncertainEffect) {
				t.Fatalf("replanner proposed a replacement for an uncertain action: %s %v", encoded, err)
			}
			continue
		}
		if err != nil || pending.IsError || pending.Meta["misty/approval"] == nil {
			t.Fatalf("write approval: %#v %v", pending, err)
		}
		raw, _ = json.Marshal(pending.Meta["misty/approval"])
		if json.Unmarshal(raw, &approval) != nil || approval.ID == "" {
			t.Fatal("missing approval")
		}
		if _, err := database.DecideCreatorToolApproval(t.Context(), user, run.ID, approval.ID, true); err != nil {
			t.Fatal(err)
		}
		outcome, err := session.CallTool(t.Context(), params)
		if callID == "lost-habit-reply" {
			uncertainEffect = effect
		} else {
			encoded, _ := json.Marshal(outcome)
			if err != nil || outcome.IsError || !strings.Contains(string(encoded), uncertainEffect) {
				t.Fatalf("replanner lost original uncertain identity: %s %v", encoded, err)
			}
		}
	}
	if writes.Load() != 2 {
		t.Fatalf("uncertain SDK effect retried: %d backend attempts", writes.Load())
	}
	dependent, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "notes.create", Arguments: map[string]any{"title": "Should not exist", "markdown": "The unconfirmed habit succeeded."}, Meta: mcp.Meta{"misty/call_id": "unsafe-dependent-summary", "misty/approval_hook_token": "unsafe-dependent-hook"}})
	if err == nil && !dependent.IsError {
		t.Fatal("dependent write executed after uncertainty")
	}
	var count int
	if err := database.Conn.QueryRow(`SELECT count(*) FROM space_notes WHERE space_id=$1 AND title_projection='Habit summary'`, space.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("Journal artifact missing: count=%d %v", count, err)
	}
	if err := database.Conn.QueryRow(`SELECT count(*) FROM space_notes WHERE space_id=$1 AND title_projection='Should not exist'`, space.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("dependent artifact escaped uncertainty gate: count=%d %v", count, err)
	}
}
