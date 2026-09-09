package db

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSpaceInterventionWaitRecoveryRevocationAndCancellation(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Space wait", uuid.NewString()+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Space wait")
	agent, err := database.EnsureAskIdentity(ctx, owner.ID, serveragent.InitialSelectedModelID)
	if err != nil {
		t.Fatal(err)
	}
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	device, err := database.RegisterTrustedDevice(owner.ID, "Original Mac", base64.RawURLEncoding.EncodeToString(public), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{"browser_tools":true}`))
	if err != nil {
		t.Fatal(err)
	}
	run, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, agent.ID, CreatorAgentRunInput{Instruction: "Read this browser", Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AttachAgentRunContext(ctx, owner.ID, run.ID, device.ID, "browser_tab", "scope-original", "Original inbox", json.RawMessage(`["browser.inspect"]`), json.RawMessage(`{"kind":"browser_tab"}`)); err != nil {
		t.Fatal(err)
	}
	// Admit both targets before execution; running work cannot change its context.
	public, _, _ = ed25519.GenerateKey(rand.Reader)
	other, err := database.RegisterTrustedDevice(owner.ID, "Other Mac", base64.RawURLEncoding.EncodeToString(public), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{"browser_tools":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AttachAgentRunContext(ctx, owner.ID, run.ID, other.ID, "browser_tab", "scope-other", "Other inbox", json.RawMessage(`["browser.inspect"]`), json.RawMessage(`{"kind":"browser_tab"}`)); err != nil {
		t.Fatal(err)
	}
	jobs, err := database.ClaimPersonalAgentTaskRunJobs(ctx, "wait-worker", 1, time.Minute)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim: %#v %v", jobs, err)
	}
	runtime := "space-wait-runtime"
	if _, err := database.ActivatePersonalAgentTaskRuntime(ctx, run.ID, "vercel-workflow", runtime); err != nil {
		t.Fatal(err)
	}
	await := func(call string) (*AIInterventionWait, error) {
		return database.AwaitAgentUserIntervention(ctx, owner.ID, run.ID, runtime, call, "hook-"+call, "scope-original", "sign_in", "Sign in in the original browser", strings.Repeat("a", 64))
	}
	first, err := await("first")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := await("first")
	if err != nil || replay.ID != first.ID {
		t.Fatalf("duplicate wait: %#v %v", replay, err)
	}
	if _, err := await("different"); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("replaced pending wait: %v", err)
	}
	busy, err := database.PersonalAgentWorkAvailability(ctx, owner.ID, agent.ID)
	if err != nil || !busy.Busy || busy.ActiveState != "awaiting_intervention" {
		t.Fatalf("waiting run not busy: %#v %v", busy, err)
	}
	if err := database.DecideAIUserIntervention(ctx, "other-user", first.ID, true); err == nil {
		t.Fatal("another user released wait")
	}
	appctx := WithAppExecutionAuthority(ctx, AppRuntimeSession{UserID: owner.ID, AppID: "example.app", Scopes: []string{"browser.inspect", "ai.write"}})
	if err := database.DecideAIUserIntervention(appctx, owner.ID, first.ID, true); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("app released own wait: %v", err)
	}
	if err := database.DecideAIUserIntervention(ctx, owner.ID, first.ID, true); err != nil {
		t.Fatal(err)
	}
	deliveries, err := database.ClaimAgentRuntimeDeliveries(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	var delivery AgentRuntimeDelivery
	var payload AgentContinuation
	for _, item := range deliveries {
		if item.RunID == run.ID && item.Operation == "intervention.resume" {
			delivery = item
			_ = json.Unmarshal(item.Payload, &payload)
		}
	}
	current, allowed, err := database.AIUserInterventionContinuation(ctx, delivery, payload)
	if err != nil || !current || !allowed {
		t.Fatalf("durable resume: %v %v %v", current, allowed, err)
	}
	replay, err = await("first")
	if err != nil || replay.State != "ready" {
		t.Fatalf("ready replay: %#v %v", replay, err)
	}
	second, err := await("second")
	if err != nil || second.ID == first.ID {
		t.Fatalf("repeated wait: %#v %v", second, err)
	}
	if _, err := await("first"); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("old wait replay bypassed a later wait: %v", err)
	}
	current, _, err = database.AIUserInterventionContinuation(ctx, delivery, payload)
	if err != nil || current {
		t.Fatalf("old wake was still current: %v %v", current, err)
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE ai_intervention_waits SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.ExpireAIUserInterventions(ctx); err != nil {
		t.Fatal(err)
	}
	replay, err = await("second")
	if err != nil || replay.State != "expired" {
		t.Fatalf("expired wait succeeded: %#v %v", replay, err)
	}
	third, err := await("third")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RevokeTrustedDevice(owner.ID, device.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.DecideAIUserIntervention(ctx, owner.ID, third.ID, true); err != nil {
		t.Fatal(err)
	}
	replay, err = await("third")
	if err != nil || replay.State != "declined" {
		t.Fatalf("revoked target resumed: %#v %v", replay, err)
	}
	fourth, err := database.AwaitAgentUserIntervention(ctx, owner.ID, run.ID, runtime, "fourth", "hook-fourth", "scope-other", "review", "Review this target", strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := database.CancelPersonalAgentTaskRunForOwner(ctx, owner.ID, run.ID)
	if err != nil || canceled.State != "canceled" {
		t.Fatalf("wait cancellation: %#v %v", canceled, err)
	}
	if err := database.DecideAIUserIntervention(ctx, owner.ID, fourth.ID, true); err == nil {
		t.Fatal("canceled wait resumed")
	}
	waits, err := database.AIUserInterventions(ctx, owner.ID)
	if err != nil || len(waits) != 0 {
		t.Fatalf("canceled request remains actionable: %#v %v", waits, err)
	}
}
