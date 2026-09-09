package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func sdkInvocationFixture(t *testing.T) (*Database, context.Context, string, cap.Invocation) {
	t.Helper()
	database := openTestDatabase(t)
	ctx := t.Context()
	user, err := database.CreateUser("Invocation owner", "invocation-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	document, key := sdkInstallFixture(t, "example.habits")
	document.Scopes = append(document.Scopes, "capabilities.invoke")
	signed, digest := sdkSignFixture(t, document, key)
	if _, err := database.InstallVerifiedSDKApp(ctx, user.ID, signed, digest); err != nil {
		t.Fatal(err)
	}
	session, err := database.CreateAppRuntimeSession(ctx, user.ID, document.AppID, security.HashToken("invocation-provider"), "", AppRuntimeSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	appctx := WithAppExecutionAuthority(ctx, *session)
	provider := document.Capabilities.Providers[0]
	if _, err := database.RegisterSDKProvider(appctx, user.ID, digest, provider); err != nil {
		t.Fatal(err)
	}
	if err := database.ReportSDKProviderAvailability(appctx, user.ID, provider.ID, cap.Availability{State: "available", ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ConfigureSDKBackendConnection(ctx, user.ID, SDKBackendConnection{UserID: user.ID, AppID: document.AppID, ID: provider.Route.ConnectionID, EndpointURL: "https://habits.example.com/execute", BearerCiphertext: []byte(strings.Repeat("encrypted", 8)), KeyVersion: 1}, 0); err != nil {
		t.Fatal(err)
	}
	target, err := database.ConfigureSDKTarget(ctx, user.ID, cap.TargetConfiguration{TargetID: uuid.NewString(), ProviderID: provider.ID, ProviderVersion: 1, Label: "My habits", Capabilities: []string{"habits.list"}, CallerApps: []string{document.AppID}})
	if err != nil {
		t.Fatal(err)
	}
	return database, appctx, user.ID, cap.Invocation{RequestID: uuid.NewString(), Capability: "habits.list", CapabilityVersion: 1, ProviderID: provider.ID, ProviderVersion: 1, TargetID: target.ID, TargetRevision: target.Revision, Input: json.RawMessage(`{}`), Deadline: time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)}
}

func TestSDKInvocationConcurrentAdmissionAndAuthorityRevocation(t *testing.T) {
	database, ctx, user, request := sdkInvocationFixture(t)
	var group sync.WaitGroup
	results := make(chan *SDKInvocationRecord, 8)
	failures := make(chan error, 8)
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			r, err := database.AdmitSDKInvocation(ctx, user, request)
			results <- r
			failures <- err
		}()
	}
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id, effect string
	for r := range results {
		if id == "" {
			id, effect = r.InvocationID, r.EffectID
		}
		if r.InvocationID != id || r.EffectID != effect {
			t.Fatal("duplicate admission changed run/effect identity")
		}
	}
	var count int
	if err := database.Conn.QueryRow(`SELECT count(*) FROM agent_runtime_deliveries WHERE run_id=$1 AND operation='invocation.start'`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("atomic dispatch: count=%d err=%v", count, err)
	}
	changed := request
	changed.Input = json.RawMessage(`{"injected":true}`)
	if _, err := database.AdmitSDKInvocation(ctx, user, changed); !errors.Is(err, ErrSpaceConflict) {
		t.Fatalf("changed retry: %v", err)
	}
	if _, err := database.SDKInvocationByRequest(t.Context(), user, request.RequestID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("request namespace escaped: %v", err)
	}
	if _, err := database.Conn.Exec(`UPDATE user_app_installations SET granted_scopes='["capabilities.read","capabilities.invoke","capabilities.providers.write"]' WHERE user_id=$1 AND app_id='example.habits'`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SDKInvocationByRequest(ctx, user, request.RequestID); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("revoked principal read protected result: %v", err)
	}
	if _, err := database.Conn.Exec(`UPDATE user_app_installations SET granted_scopes='["capabilities.read","capabilities.invoke","capabilities.providers.write","habits.list"]' WHERE user_id=$1 AND app_id='example.habits'`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AdmitSDKInvocation(ctx, user, request); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("restoring grant revived old run principal: %v", err)
	}
}

func TestSDKInvocationApprovalWaitAndCancellation(t *testing.T) {
	database, ctx, user, request := sdkInvocationFixture(t)
	record, err := database.AdmitSDKInvocation(ctx, user, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateAIInvocationRuntime(t.Context(), record.InvocationID, "vercel-workflow", "runtime-sdk"); err != nil {
		t.Fatal(err)
	}
	review := ProtectedSDKApproval{EffectID: record.EffectID, Digest: strings.Repeat("a", 64), Ciphertext: []byte(strings.Repeat("protected", 8))}
	if _, _, err := database.RequireSDKToolApproval(ctx, user, record.InvocationID, record.EffectID, "sdk.test", "hash", "hook", "Missing review"); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("unreviewable approval admitted: %v", err)
	}
	current, err := database.AIInvocationByID(t.Context(), user, record.InvocationID)
	if err != nil || current.State != "running" {
		t.Fatalf("failed review persisted a wait: %#v %v", current, err)
	}
	approval, allowed, err := database.RequireSDKToolApproval(ctx, user, record.InvocationID, record.EffectID, "sdk.test", "hash", "hook", "Review habit action", review)
	if err != nil || allowed {
		t.Fatalf("approval: %#v %v %v", approval, allowed, err)
	}
	_, stored, err := database.SDKApprovalReview(t.Context(), user, approval.ID)
	if err != nil || string(stored.Ciphertext) != string(review.Ciphertext) {
		t.Fatalf("review not committed with wait: %#v %v", stored, err)
	}
	if _, _, err := database.SDKApprovalReview(ctx, user, approval.ID); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("app read protected review: %v", err)
	}
	changedReview := review
	changedReview.Digest = strings.Repeat("b", 64)
	if _, _, err := database.RequireSDKToolApproval(ctx, user, record.InvocationID, record.EffectID, "sdk.test", "hash", "hook", "Changed review", changedReview); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("replaced approval review: %v", err)
	}
	retryReview := review
	retryReview.Ciphertext = []byte(strings.Repeat("new-random-nonce", 8))
	if _, _, err := database.RequireSDKToolApproval(ctx, user, record.InvocationID, record.EffectID, "sdk.test", "hash", "hook", "Retry review", retryReview); err != nil {
		t.Fatal(err)
	}
	_, stored, err = database.SDKApprovalReview(t.Context(), user, approval.ID)
	if err != nil || string(stored.Ciphertext) != string(review.Ciphertext) {
		t.Fatalf("retry replaced protected review: %#v %v", stored, err)
	}
	if err := database.DecideSDKToolApproval(ctx, user, record.InvocationID, approval.ID, true); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("self approval: %v", err)
	}
	if err := database.DecideSDKToolApproval(t.Context(), user, record.InvocationID, approval.ID, true); err != nil {
		t.Fatal(err)
	}
	again, allowed, err := database.RequireSDKToolApproval(ctx, user, record.InvocationID, record.EffectID, "sdk.test", "hash", "hook", "Review", review)
	if err != nil || !allowed || again.ID != approval.ID {
		t.Fatalf("approved resume: %#v %v %v", again, allowed, err)
	}
	if _, _, err := database.RequireSDKToolApproval(ctx, user, record.InvocationID, record.EffectID, "sdk.test", "changed", "hook", "Review", review); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("approval changed input: %v", err)
	}
	if err := database.RequestSDKCancellation(ctx, user, record.InvocationID); err != nil {
		t.Fatal(err)
	}
	updated, err := database.SDKInvocationForRun(t.Context(), user, record.InvocationID)
	if err != nil || !updated.CancelRequested || updated.State != "running" {
		t.Fatalf("premature cancellation: %#v %v", updated, err)
	}
	if err := database.DecideSDKToolApproval(t.Context(), user, record.InvocationID, approval.ID, true); err == nil {
		t.Fatal("cancelled action resumed")
	}
	if err := database.PublishSDKCompletion(t.Context(), user, record.InvocationID, "success", "completed", []byte(strings.Repeat("protected", 8))); err == nil {
		t.Fatal("success without confirmed effect")
	}
}

func TestSDKPendingApprovalDiscovery(t *testing.T) {
	database, appctx, user, request := sdkInvocationFixture(t)
	var approvals []*AgentToolApproval
	for range 3 {
		request.RequestID = uuid.NewString()
		run, err := database.AdmitSDKInvocation(appctx, user, request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.ActivateAIInvocationRuntime(t.Context(), run.InvocationID, "vercel-workflow", uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		review := ProtectedSDKApproval{EffectID: run.EffectID, Digest: strings.Repeat("a", 64), Ciphertext: []byte(strings.Repeat("protected", 8))}
		approval, _, err := database.RequireSDKToolApproval(appctx, user, run.InvocationID, run.EffectID, "sdk.test", "hash", "hook", "Review habit action", review)
		if err != nil {
			t.Fatal(err)
		}
		approvals = append(approvals, approval)
	}
	if _, err := database.SDKPendingApprovals(appctx, user, "", 2); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("app discovered trusted approvals: %v", err)
	}
	first, err := database.SDKPendingApprovals(t.Context(), user, "", 2)
	if err != nil || len(first.Approvals) != 2 || first.NextCursor == "" {
		t.Fatalf("first page: %#v %v", first, err)
	}
	second, err := database.SDKPendingApprovals(t.Context(), user, first.NextCursor, 2)
	if err != nil || len(second.Approvals) != 1 || second.NextCursor != "" || second.Approvals[0].ID == first.Approvals[0].ID || second.Approvals[0].ID == first.Approvals[1].ID {
		t.Fatalf("pagination lost/repeated waits: %#v %v", second, err)
	}
	if err := database.DecideSDKToolApproval(t.Context(), user, approvals[0].RunID, approvals[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if err := database.RequestSDKCancellation(appctx, user, approvals[1].RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn.Exec(`UPDATE agent_run_tool_approvals SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, approvals[2].ID); err != nil {
		t.Fatal(err)
	}
	settled, err := database.SDKPendingApprovals(t.Context(), user, "", 20)
	if err != nil || len(settled.Approvals) != 0 {
		t.Fatalf("decided/cancelled/expired waits advertised: %#v %v", settled, err)
	}
}
