package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func TestSDKTargetPinsAccountConfigurationAndRejectsStaleRevision(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Target owner", "target-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	document, key := sdkInstallFixture(t, "example.habits")
	signed, digest := sdkSignFixture(t, document, key)
	if _, err := database.InstallVerifiedSDKApp(ctx, user.ID, signed, digest); err != nil {
		t.Fatal(err)
	}
	session, err := database.CreateAppRuntimeSession(ctx, user.ID, document.AppID, security.HashToken("target-provider"), "", AppRuntimeSessionTTL)
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
	request := cap.TargetConfiguration{TargetID: uuid.NewString(), ProviderID: provider.ID, ProviderVersion: 1, Label: "My habits account", Capabilities: []string{"habits.list"}, CallerApps: []string{document.AppID}}
	if _, err := database.ConfigureSDKTarget(ctx, user.ID, request); !errors.Is(err, ErrSDKProviderUnavailable) {
		t.Fatalf("target without connection: %v", err)
	}
	connection := SDKBackendConnection{UserID: user.ID, AppID: document.AppID, ID: provider.Route.ConnectionID, EndpointURL: "https://habits.example.com/execute", BearerCiphertext: []byte(strings.Repeat("encrypted", 8)), KeyVersion: 1}
	if _, err := database.ConfigureSDKBackendConnection(appctx, user.ID, connection, 0); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("self-configured account: %v", err)
	}
	if _, err := database.ConfigureSDKBackendConnection(ctx, user.ID, connection, 0); err != nil {
		t.Fatal(err)
	}
	target, err := database.ConfigureSDKTarget(ctx, user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ConfigureSDKTarget(appctx, user.ID, request); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("self-granted target: %v", err)
	}
	resolved, err := database.ResolveSDKBoundCapability(appctx, user.ID, target.ID, 1, "habits.list", 1)
	if err != nil || resolved.Connection.Revision != 1 || resolved.Target.AppID != document.AppID {
		t.Fatalf("resolve: %#v %v", resolved, err)
	}
	page, err := database.DiscoverSDKProviders(appctx, user.ID, SDKProviderDiscovery{TargetID: target.ID})
	if err != nil || len(page.Providers) != 1 {
		t.Fatalf("target discovery: %#v %v", page, err)
	}
	if _, err := database.ConfigureSDKTarget(ctx, user.ID, request); !errors.Is(err, ErrSDKVersionConflict) {
		t.Fatalf("stale edit: %v", err)
	}
	if _, err := database.ConfigureSDKBackendConnection(ctx, user.ID, connection, 0); !errors.Is(err, ErrSDKVersionConflict) {
		t.Fatalf("stale connection edit: %v", err)
	}
	connection.EndpointURL = "https://another-account.example.com/execute"
	if _, err := database.ConfigureSDKBackendConnection(ctx, user.ID, connection, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveSDKBoundCapability(appctx, user.ID, target.ID, 1, "habits.list", 1); !errors.Is(err, ErrSDKProviderUnavailable) {
		t.Fatalf("silently switched account: %v", err)
	}
	request.ExpectedRevision = 1
	target, err = database.ConfigureSDKTarget(ctx, user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if target.Revision != 2 {
		t.Fatal("target revision did not advance")
	}
	if _, err := database.ResolveSDKBoundCapability(appctx, user.ID, target.ID, 1, "habits.list", 1); !errors.Is(err, ErrSDKProviderUnavailable) {
		t.Fatalf("old revision resolved: %v", err)
	}
	resolved, err = database.ResolveSDKBoundCapability(appctx, user.ID, target.ID, 2, "habits.list", 1)
	if err != nil || resolved.Connection.Revision != 2 {
		t.Fatalf("refreshed target: %#v %v", resolved, err)
	}
	if err := database.RevokeSDKBackendConnection(ctx, user.ID, document.AppID, connection.ID); err != nil {
		t.Fatal(err)
	}
	targets, err := database.ResolveSDKTargets(appctx, user.ID, cap.TargetResolve{Capability: "habits.list"})
	if err != nil || len(targets) != 0 {
		t.Fatalf("revoked connection discoverable: %#v %v", targets, err)
	}
}

func TestSDKTargetRequiresExplicitCallerAndCurrentSpaceAccess(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Target sharing", "target-sharing@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other target", "target-other@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	firstSpace := createTestSpace(t, database, ctx, user.ID, "First")
	secondSpace := createTestSpace(t, database, ctx, user.ID, "Second")
	document, key := sdkInstallFixture(t, "example.habits")
	signed, digest := sdkSignFixture(t, document, key)
	if _, err := database.InstallVerifiedSDKApp(ctx, user.ID, signed, digest); err != nil {
		t.Fatal(err)
	}
	session, err := database.CreateAppRuntimeSession(ctx, user.ID, document.AppID, security.HashToken("sharing-provider"), "", AppRuntimeSessionTTL)
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
	connection := SDKBackendConnection{UserID: user.ID, AppID: document.AppID, ID: provider.Route.ConnectionID, EndpointURL: "https://habits.example.com/execute", BearerCiphertext: []byte(strings.Repeat("encrypted", 8)), KeyVersion: 1}
	if _, err := database.ConfigureSDKBackendConnection(ctx, user.ID, connection, 0); err != nil {
		t.Fatal(err)
	}
	request := cap.TargetConfiguration{TargetID: uuid.NewString(), ProviderID: provider.ID, ProviderVersion: 1, SpaceID: firstSpace.ID, Label: "Space habits", Capabilities: []string{"habits.list"}, CallerApps: []string{}}
	target, err := database.ConfigureSDKTarget(ctx, user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveSDKBoundCapability(appctx, user.ID, target.ID, 1, "habits.list", 1); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("implicit app access: %v", err)
	}
	if _, err := database.ResolveSDKBoundCapability(ctx, other.ID, target.ID, 1, "habits.list", 1); !errors.Is(err, ErrSDKProviderUnavailable) {
		t.Fatalf("cross-user target: %v", err)
	}
	request.ExpectedRevision = 1
	request.CallerApps = []string{document.AppID}
	target, err = database.ConfigureSDKTarget(ctx, user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	session.SpaceID = secondSpace.ID
	if _, err := database.ResolveSDKBoundCapability(WithAppExecutionAuthority(ctx, *session), user.ID, target.ID, 2, "habits.list", 1); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("cross-Space target: %v", err)
	}
	session.SpaceID = firstSpace.ID
	if _, err := database.ResolveSDKBoundCapability(WithAppExecutionAuthority(ctx, *session), user.ID, target.ID, 2, "habits.list", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn.ExecContext(ctx, `DELETE FROM space_members WHERE space_id=$1 AND user_id=$2`, firstSpace.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveSDKBoundCapability(WithAppExecutionAuthority(ctx, *session), user.ID, target.ID, 2, "habits.list", 1); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("removed membership retained target access: %v", err)
	}
	if err := database.RevokeSDKTarget(appctx, user.ID, target.ID); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("app mutated user target: %v", err)
	}
	if err := database.RevokeSDKTarget(ctx, user.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveSDKBoundCapability(ctx, user.ID, target.ID, 2, "habits.list", 1); !errors.Is(err, ErrSDKProviderUnavailable) {
		t.Fatalf("revoked target: %v", err)
	}
}
