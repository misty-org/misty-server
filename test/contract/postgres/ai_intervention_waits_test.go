package db

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAIInterventionWaitPreservesTargetAndTrustedDecision(t *testing.T) {
	database := openTestDatabase(t)
	owner, err := database.CreateUser("Intervention", uuid.NewString()+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, t.Context(), owner.ID, "Intervention")
	run := "invocation_" + uuid.NewString()
	runtime := "wait-runtime"
	_, _, err = database.CreateAIInvocationRecord(t.Context(), AIInvocationRecord{ID: run, UserID: owner.ID, SpaceID: space.ID, Mode: "quick", SurfaceID: "settings", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	key, _, _ := ed25519.GenerateKey(rand.Reader)
	device, err := database.RegisterTrustedDevice(owner.ID, "Wait Mac", base64.RawURLEncoding.EncodeToString(key), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{"browser_tools":true}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.AttachAIInvocationContext(t.Context(), owner.ID, run, space.ID, device.ID, "browser_tab", "scope-user-wait", "Personal inbox", json.RawMessage(`["browser.inspect"]`), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.ActivateAIInvocationRuntime(t.Context(), run, "vercel-workflow", runtime); err != nil {
		t.Fatal(err)
	}
	await := func(call, hook, digest string) (*AIInterventionWait, error) {
		return database.AwaitAIUserIntervention(t.Context(), owner.ID, run, runtime, call, hook, "scope-user-wait", "sign_in", "Sign in to your personal inbox", digest)
	}
	wait, err := await("call-1", "hook-1", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if wait.State != "pending" || wait.TargetLabel != "Personal inbox" {
		t.Fatalf("wait: %#v", wait)
	}
	record, err := database.AIInvocationByID(t.Context(), owner.ID, run)
	if err != nil || record.State != "awaiting_intervention" {
		t.Fatalf("state: %#v %v", record, err)
	}
	if replay, err := await("call-1", "hook-1", strings.Repeat("a", 64)); err != nil || replay.ID != wait.ID {
		t.Fatalf("wait replay: %#v %v", replay, err)
	}
	if _, err := await("call-1", "hook-1", strings.Repeat("b", 64)); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("changed call admitted: %v", err)
	}
	if _, err := await("call-2", "hook-2", strings.Repeat("a", 64)); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("another wait replaced pending: %v", err)
	}
	if err := database.DecideAIUserIntervention(t.Context(), "wrong-user", wait.ID, true); err == nil {
		t.Fatal("cross-user decision")
	}
	appctx := WithAppExecutionAuthority(t.Context(), AppRuntimeSession{UserID: owner.ID, AppID: "sample", Scopes: []string{"ai.write", "browser.inspect"}})
	if err := database.DecideAIUserIntervention(appctx, owner.ID, wait.ID, true); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("app approved itself: %v", err)
	}
	if err := database.DecideAIUserIntervention(t.Context(), owner.ID, wait.ID, true); err != nil {
		t.Fatal(err)
	}
	deliveries, err := database.ClaimAgentRuntimeDeliveries(t.Context(), 20)
	if err != nil {
		t.Fatal(err)
	}
	var delivery AgentRuntimeDelivery
	var payload AgentContinuation
	for _, d := range deliveries {
		if d.Operation == "intervention.resume" && d.RunID == run {
			delivery = d
			_ = json.Unmarshal(d.Payload, &payload)
		}
	}
	if delivery.ID == "" || !payload.Available {
		t.Fatalf("no durable affirmative resume: %#v", deliveries)
	}
	current, allowed, err := database.AIUserInterventionContinuation(t.Context(), delivery, payload)
	if err != nil || !current || !allowed {
		t.Fatalf("resume invalid: %v %v %v", current, allowed, err)
	}
	replay, err := await("call-1", "next-attempt-hook", strings.Repeat("a", 64))
	if err != nil || replay.State != "ready" {
		t.Fatalf("resume original call: %#v %v", replay, err)
	}
	current, _, err = database.AIUserInterventionContinuation(t.Context(), delivery, payload)
	if err != nil || current {
		t.Fatalf("old resume survived consumed call: %v %v", current, err)
	}
	second, err := await("call-2", "hook-2", strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.TestingSpaceTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE ai_intervention_waits SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, second.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.ExpireAIUserInterventions(t.Context()); err != nil {
		t.Fatal(err)
	}
	expired, err := await("call-2", "hook-after-expiry", strings.Repeat("c", 64))
	if err != nil || expired.State != "expired" {
		t.Fatalf("expired wait resumed positively: %#v %v", expired, err)
	}
}
