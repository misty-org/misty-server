package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// A run names its Space and profile and works on the computer it was sent from, and
// a schedule keeps the profile it was made with.

func TestSchedulesAndRunsCarryProfileAndOldDeviceKeepingIsGone(t *testing.T) {
	f := newOnServerFixture(t)
	ctx := context.Background()
	// Nothing about a chat being kept on a device remains.
	for _, column := range []string{"kept_on_device_id", "legacy"} {
		if got := f.scalar(t, `SELECT count(*)::text FROM information_schema.columns WHERE table_name='misty_ask_conversations' AND column_name=$1`, column); got != "0" {
			t.Fatalf("misty_ask_conversations still has %s", column)
		}
	}
	if got := f.scalar(t, `SELECT is_nullable FROM information_schema.columns WHERE table_name='misty_ask_conversations' AND column_name='space_id'`); got != "NO" {
		t.Fatalf("space_id nullable = %s", got)
	}
	// A conversation written without a Space joins its owner's personal Space.
	conversationID, err := f.database.CreatePersonalAgentConversation(ctx, f.owner.ID, "", f.agentID)
	if err != nil {
		t.Fatal(err)
	}
	personal, err := f.database.MemorySpace(ctx, f.owner.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if space, err := f.database.AgentConversationSpace(ctx, f.owner.ID, conversationID); err != nil || space != personal {
		t.Fatalf("conversation Space = %q, %v, want %q", space, err, personal)
	}

	// A run names its profile, and defaults to the first one.
	id := "invocation_" + uuid.NewString()
	run, _, err := f.database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{
		ID: id, UserID: f.owner.ID, ConversationID: conversationID, Profile: "work", SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "queued",
		IdempotencyKey: id, RequestPayload: json.RawMessage(`{"prompt":"hi"}`), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.scalar(t, `SELECT profile||':'||space_id FROM ai_invocations WHERE id=$1`, run.ID); got != "work:"+personal {
		t.Fatalf("run recorded %q", got)
	}

	// A run is pinned from admission to the computer it was sent from; continuing
	// from another computer runs there, and a run the server starts stays put.
	finish := func(id string) {
		f.exec(t, `UPDATE ai_invocations SET state='completed' WHERE id=$1`, id)
	}
	finish(run.ID)
	admit := func(endpoint string) *db.AIInvocationRecord {
		id := "invocation_" + uuid.NewString()
		stored, _, err := f.database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{
			ID: id, UserID: f.owner.ID, ConversationID: conversationID, RuntimeEndpoint: endpoint, SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "queued",
			IdempotencyKey: id, RequestPayload: json.RawMessage(`{"prompt":"hi"}`), ExpiresAt: time.Now().Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		return &stored
	}
	first := admit("device:one")
	finish(first.ID)
	if got := f.scalar(t, `SELECT runtime_endpoint FROM ai_invocations WHERE id=$1`, first.ID); got != "device:one" {
		t.Fatalf("pinned to %q", got)
	}
	second := admit("device:two")
	finish(second.ID)
	if got := f.scalar(t, `SELECT runtime_endpoint FROM ai_invocations WHERE id=$1`, second.ID); got != "device:two" {
		t.Fatalf("continued from another computer, pinned to %q", got)
	}
	continued := admit("")
	if got := f.scalar(t, `SELECT runtime_endpoint FROM ai_invocations WHERE id=$1`, continued.ID); got != "device:two" {
		t.Fatalf("a run the server started is pinned to %q", got)
	}
}

// A schedule keeps the profile it was made with, a computer without that profile
// can't claim its run, and the schedule says why.
func TestScheduleStoresProfileAndClaimingHonoursIt(t *testing.T) {
	f := newOnServerFixture(t)
	ctx := context.Background()
	definition := db.AgentMethodDefinition{Title: "Morning brief", Instructions: "Write a morning brief.", Target: "cloud", Inputs: []db.AgentMethodInput{}}
	method, err := f.database.SaveAgentMethod(ctx, f.owner.ID, db.AgentMethod{AgentID: f.agentID, Kind: "workflow", Enabled: true, Definition: definition}, 0)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Put("/methods/{methodID}/schedule", f.service.WorkflowSchedule())
	router.Post("/schedules/{scheduleID}/claim", f.service.ClaimDeviceScheduledRun())
	token := newConversationTestBearerToken(t, f.database, f.owner.ID)
	device := "device_" + uuid.NewString()
	f.exec(t, `INSERT INTO trusted_devices(id,user_id,name,public_key) VALUES($1,$2,'Laptop',repeat('k',44))`, device, f.owner.ID)

	save := func(body map[string]any) db.WorkflowSchedule {
		t.Helper()
		body["timezone"], body["rules"], body["inputs"] = "UTC", []any{map[string]any{"frequency": "daily", "times": []string{"09:00"}}}, map[string]any{}
		rec := performConversationRequest(t, router, http.MethodPut, "/methods/"+method.ID+"/schedule", token, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("schedule = %d, %s", rec.Code, rec.Body)
		}
		var saved struct {
			Schedule db.WorkflowSchedule `json:"schedule"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
			t.Fatal(err)
		}
		return saved.Schedule
	}
	// The first profile is the default; a chosen one is kept, and saving again without one keeps it.
	if got := save(map[string]any{"device_id": device}); got.Profile != "personal" {
		t.Fatalf("default profile = %q", got.Profile)
	}
	if got := save(map[string]any{"device_id": device, "profile": "work"}); got.Profile != "work" {
		t.Fatalf("chosen profile = %q", got.Profile)
	}
	schedule := save(map[string]any{"device_id": device})
	if schedule.Profile != "work" {
		t.Fatalf("kept profile = %q", schedule.Profile)
	}
	if rec := performConversationRequest(t, router, http.MethodPut, "/methods/"+method.ID+"/schedule", token, map[string]any{"profile": "../etc", "timezone": "UTC", "rules": []any{map[string]any{"frequency": "daily", "times": []string{"09:00"}}}, "inputs": map[string]any{}}); rec.Code == http.StatusOK {
		t.Fatal("a bad profile id was saved")
	}
	// Its run comes due and waits for the computer.
	f.exec(t, `UPDATE workflow_schedules SET state='awaiting_device',due_at=NOW() WHERE id=$1`, schedule.ID)
	claimPath := "/schedules/" + schedule.ID + "/claim"
	rec := performConversationRequest(t, router, http.MethodPost, claimPath, token, map[string]any{"device_id": device, "profiles": []string{"personal"}})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "schedule_profile_missing") {
		t.Fatalf("claim without the profile = %d, %s", rec.Code, rec.Body)
	}
	stored, err := f.database.WorkflowScheduleByMethod(ctx, f.owner.ID, method.ID)
	if err != nil || stored.State != "awaiting_device" || !strings.Contains(stored.LastError, "profile") {
		t.Fatalf("schedule after a refused claim = %+v, %v", stored, err)
	}
	rec = performConversationRequest(t, router, http.MethodPost, claimPath, token, map[string]any{"device_id": device, "profiles": []string{"personal", "work"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"profile":"work"`) {
		t.Fatalf("claim with the profile = %d, %s", rec.Code, rec.Body)
	}
	stored, err = f.database.WorkflowScheduleByMethod(ctx, f.owner.ID, method.ID)
	if err != nil || stored.State != "device_claimed" || stored.LastError != "" {
		t.Fatalf("schedule after a claim = %+v, %v", stored, err)
	}
}
