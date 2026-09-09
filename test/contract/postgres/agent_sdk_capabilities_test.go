package db

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func TestAgentSDKAdmissionPinsVersionsAndDelegationScope(t *testing.T) {
	database, appctx, user, request := sdkInvocationFixture(t)
	ctx := t.Context()
	space := createTestSpace(t, database, ctx, user, "SDK conversation")
	agent, err := database.EnsureAskIdentity(ctx, user, serveragent.InitialSelectedModelID)
	if err != nil {
		t.Fatal(err)
	}
	create := func(parent string) *SpaceRun {
		t.Helper()
		r, err := database.CreateCreatorAgentRun(ctx, user, space.ID, agent.ID, CreatorAgentRunInput{Instruction: "Record a habit and summarize it", ParentRunID: parent})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	accountOnly := create("")
	pins, err := database.AgentSDKCapabilityBindings(ctx, user, accountOnly.ID)
	if err != nil || len(pins) != 0 {
		t.Fatalf("implicit account data in Space: %#v %v", pins, err)
	}
	configure := cap.TargetConfiguration{TargetID: request.TargetID, ExpectedRevision: 1, ProviderID: request.ProviderID, ProviderVersion: 1, SpaceID: space.ID, Label: "Space habits", Capabilities: []string{"habits.list"}, CallerApps: []string{"example.habits"}}
	if _, err := database.ConfigureSDKTarget(ctx, user, configure); err != nil {
		t.Fatal(err)
	}
	run := create("")
	pins, err = database.AgentSDKCapabilityBindings(ctx, user, run.ID)
	if err != nil || len(pins) != 1 || pins[0].TargetRevision != 2 {
		t.Fatalf("admission pins: %#v %v", pins, err)
	}
	ai, _, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{ID: "invocation_" + uuid.NewString(), UserID: user, SpaceID: space.ID, SurfaceID: "journal", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveAgentSDKCapability(ctx, user, run.ID, pins[0]); err != nil {
		t.Fatal(err)
	}
	if err := database.ReportSDKProviderAvailability(appctx, user, request.ProviderID, cap.Availability{State: "authentication_required", ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveAgentSDKCapability(ctx, user, run.ID, pins[0]); !errors.Is(err, ErrSDKProviderUnavailable) {
		t.Fatalf("unavailable provider executed: %v", err)
	}
	whileUnavailable := create("")
	waitingPins, err := database.AgentSDKCapabilityBindings(ctx, user, whileUnavailable.ID)
	if err != nil || len(waitingPins) != 1 {
		t.Fatalf("unavailable implementation lost its pin: %#v %v", waitingPins, err)
	}
	if err := database.ReportSDKProviderAvailability(appctx, user, request.ProviderID, cap.Availability{State: "available", ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveAgentSDKCapability(ctx, user, whileUnavailable.ID, waitingPins[0]); err != nil {
		t.Fatalf("same pinned target did not recover availability: %v", err)
	}

	forged := pins[0]
	forged.TargetID = uuid.NewString()
	if _, err := database.ResolveAgentSDKCapability(ctx, user, run.ID, forged); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("forged target: %v", err)
	}
	other, err := database.CreateUser("Other SDK owner", "other-sdk-agent@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveAgentSDKCapability(ctx, other.ID, run.ID, pins[0]); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("cross-user pin: %v", err)
	}
	configure.ExpectedRevision = 2
	configure.Label = "Changed account configuration"
	if _, err := database.ConfigureSDKTarget(ctx, user, configure); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveAgentSDKCapability(ctx, user, run.ID, pins[0]); !errors.Is(err, ErrSDKProviderUnavailable) {
		t.Fatalf("pinned run switched configuration: %v", err)
	}
	child := create(run.ID)
	inherited, err := database.AgentSDKCapabilityBindings(ctx, user, child.ID)
	if err != nil || len(inherited) != 1 || inherited[0].TargetRevision != 2 {
		t.Fatalf("delegation expanded to current target: %#v %v", inherited, err)
	}
	linked, err := database.CreateCreatorAgentRun(ctx, user, space.ID, agent.ID, CreatorAgentRunInput{Instruction: "Continue this invocation", AIInvocationID: ai.ID})
	if err != nil {
		t.Fatal(err)
	}
	linkedPins, err := database.AgentSDKCapabilityBindings(ctx, user, linked.ID)
	if err != nil || len(linkedPins) != 1 || linkedPins[0].TargetRevision != 2 {
		t.Fatalf("AI delegation resnapshotted current targets: %#v %v", linkedPins, err)
	}
	current := create("")
	refreshed, err := database.AgentSDKCapabilityBindings(ctx, user, current.ID)
	if err != nil || len(refreshed) != 1 || refreshed[0].TargetRevision != 3 {
		t.Fatalf("new admission missing current target: %#v %v", refreshed, err)
	}
	old, err := database.AgentSDKCapabilityBindings(ctx, user, accountOnly.ID)
	if err != nil || len(old) != 0 {
		t.Fatalf("old empty run acquired provider: %#v %v", old, err)
	}
}

func TestAIInvocationSDKScopeIsPinnedAtAdmission(t *testing.T) {
	database, appctx, user, request := sdkInvocationFixture(t)
	ctx := t.Context()
	makeRecord := func(key string) AIInvocationRecord {
		return AIInvocationRecord{ID: "invocation_" + uuid.NewString(), UserID: user, SurfaceID: "settings", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: key, RequestPayload: json.RawMessage(`{"mode":"quick","surface_id":"settings","trigger":"message","prompt":"List recorded habits","context":[],"timezone":"UTC","idempotency_key":"sdk-test"}`), ExpiresAt: time.Now().Add(time.Hour), DispatchRuntime: true}
	}
	if _, _, err := database.CreateAIInvocationRecord(appctx, makeRecord("app-no-ai")); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("missing AI authority admitted: %v", err)
	}
	run, created, err := database.CreateAIInvocationRecord(ctx, makeRecord("account-sdk"))
	if err != nil || !created {
		t.Fatalf("AI admission: %v %v", created, err)
	}
	pins, err := database.AgentSDKCapabilityBindings(ctx, user, run.ID)
	if err != nil || len(pins) != 1 || pins[0].TargetID != request.TargetID {
		t.Fatalf("AI target snapshot: %#v %v", pins, err)
	}
	if _, err := database.ResolveAgentSDKCapability(ctx, user, run.ID, pins[0]); err != nil {
		t.Fatal(err)
	}
	changed := cap.TargetConfiguration{TargetID: request.TargetID, ExpectedRevision: 1, ProviderID: request.ProviderID, ProviderVersion: 1, Label: "Different account configuration", Capabilities: []string{"habits.list"}, CallerApps: []string{"example.habits"}}
	if _, err := database.ConfigureSDKTarget(ctx, user, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveAgentSDKCapability(ctx, user, run.ID, pins[0]); !errors.Is(err, ErrSDKProviderUnavailable) {
		t.Fatalf("AI target switched after admission: %v", err)
	}
	if _, err := database.ActivateAIInvocationRuntime(ctx, run.ID, "vercel-workflow", "ai-sdk-runtime"); err != nil {
		t.Fatal(err)
	}
	// App principals cannot approve a quick request; trusted decisions resume the same wait.
	review := ProtectedSDKApproval{EffectID: uuid.NewString(), Digest: strings.Repeat("a", 64), Ciphertext: []byte(strings.Repeat("protected", 8))}
	approval, _, err := database.RequireSDKToolApproval(ctx, user, run.ID, review.EffectID, "sdk.pinned", "hash", "hook-ai", "Review action", review)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DecideSDKToolApproval(appctx, user, run.ID, approval.ID, true); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("AI app self-approved: %v", err)
	}
	if err := database.DecideSDKToolApproval(ctx, user, run.ID, approval.ID, true); err != nil {
		t.Fatal(err)
	}
	delivery := AgentRuntimeDelivery{UserID: user, RunID: run.ID, Operation: "approval.resume"}
	payload := AgentContinuation{RuntimeID: "ai-sdk-runtime", ApprovalID: approval.ID, HookToken: "hook-ai", Approved: true}
	current, err := database.AgentContinuationCurrent(ctx, delivery, payload)
	if err != nil || !current {
		t.Fatalf("AI approval resume not current: %v %v", current, err)
	}
	if err := database.FinishAgentContinuation(ctx, delivery, payload); err != nil {
		t.Fatal(err)
	}
	current, err = database.AgentContinuationCurrent(ctx, delivery, payload)
	if err != nil || current {
		t.Fatalf("consumed wait stayed current: %v %v", current, err)
	}
}

func TestAIInvocationSDKAppPermissionCeilingAndLinkedAuthority(t *testing.T) {
	database, _, user, request := sdkInvocationFixture(t)
	ctx := t.Context()
	space := createTestSpace(t, database, ctx, user, "Scoped requester")
	agent, err := database.EnsureAskIdentity(ctx, user, serveragent.InitialSelectedModelID)
	if err != nil {
		t.Fatal(err)
	}
	document, key := sdkInstallFixture(t, "example.requester")
	document.Scopes = append(document.Scopes, "ai.write", "capabilities.invoke")
	signed, digest := sdkSignFixture(t, document, key)
	if _, err := database.InstallVerifiedSDKApp(ctx, user, signed, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ConfigureSDKTarget(ctx, user, cap.TargetConfiguration{TargetID: request.TargetID, ExpectedRevision: 1, ProviderID: request.ProviderID, ProviderVersion: 1, SpaceID: space.ID, Label: "Shared habits", Capabilities: []string{"habits.list"}, CallerApps: []string{"example.requester"}}); err != nil {
		t.Fatal(err)
	}
	setScopes := func(scopes []string) {
		t.Helper()
		raw, _ := json.Marshal(scopes)
		if _, err := database.Conn.Exec(`UPDATE user_app_installations SET granted_scopes=$1 WHERE user_id=$2 AND app_id='example.requester'`, raw, user); err != nil {
			t.Fatal(err)
		}
	}
	admit := func() AIInvocationRecord {
		t.Helper()
		session, err := database.CreateAppRuntimeSession(ctx, user, document.AppID, security.HashToken(uuid.NewString()), "", AppRuntimeSessionTTL)
		if err != nil {
			t.Fatal(err)
		}
		record, _, err := database.CreateAIInvocationRecord(WithAppExecutionAuthority(ctx, *session), AIInvocationRecord{ID: "invocation_" + uuid.NewString(), UserID: user, SpaceID: space.ID, SurfaceID: "journal", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	setScopes([]string{"ai.write"})
	restricted := admit()
	pins, err := database.AgentSDKCapabilityBindings(ctx, user, restricted.ID)
	if err != nil || len(pins) != 0 {
		t.Fatalf("AI-only app gained provider scope: %#v %v", pins, err)
	}
	setScopes(document.Scopes)
	expanded := admit()
	pins, err = database.AgentSDKCapabilityBindings(ctx, user, expanded.ID)
	if err != nil || len(pins) != 1 {
		t.Fatalf("explicit provider grant not admitted: %#v %v", pins, err)
	}
	oldPins, err := database.AgentSDKCapabilityBindings(ctx, user, restricted.ID)
	if err != nil || len(oldPins) != 0 {
		t.Fatalf("later grants expanded an old run: %#v %v", oldPins, err)
	}
	linked, err := database.CreateCreatorAgentRun(ctx, user, space.ID, agent.ID, CreatorAgentRunInput{Instruction: "Continue the app request", AIInvocationID: expanded.ID})
	if err != nil {
		t.Fatal(err)
	}
	authority, err := AppAuthorityFromPayload(linked.Input)
	if err != nil || authority == nil || authority.AppID != document.AppID {
		t.Fatalf("linked AI child shed app principal: %#v %v", authority, err)
	}
	linkedPins, err := database.AgentSDKCapabilityBindings(ctx, user, linked.ID)
	if err != nil || len(linkedPins) != 1 {
		t.Fatalf("linked bindings: %#v %v", linkedPins, err)
	}
	if _, err := database.ResolveAgentSDKCapability(ctx, user, linked.ID, linkedPins[0]); err != nil {
		t.Fatal(err)
	}
	setScopes([]string{"ai.write"})
	if _, err := database.ResolveAgentSDKCapability(ctx, user, linked.ID, linkedPins[0]); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("linked run survived app grant revocation: %v", err)
	}
}
