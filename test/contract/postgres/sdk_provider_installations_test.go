package db

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func sdkInstallFixture(t *testing.T, appID string) (cap.InstallDocument, ed25519.PrivateKey) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return cap.InstallDocument{AppID: appID, Version: "1.0.0", PermissionVersion: 1, Scopes: []string{"capabilities.providers.write", "capabilities.read", "habits.list"}, Capabilities: cap.Manifest{Protocol: 1, Providers: []cap.Provider{{ID: appID + "/backend", Version: 1, Label: "Habits", Route: cap.Route{Kind: "backend", ConnectionID: "10000000-0000-4000-8000-000000000001"}, Capabilities: []cap.Definition{{Name: "habits.list", Version: 1, Description: "List recorded habits", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"string"}}`), RequiredScopes: []string{"habits.list"}, Effects: cap.Effects{Kind: "read", Incidental: []string{}, Approval: "none", Retry: "read_only"}}}}}}}, key
}
func sdkSignFixture(t *testing.T, d cap.InstallDocument, key ed25519.PrivateKey) (cap.SignedManifest, string) {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	signed := cap.SignedManifest{Document: string(raw), PublicKey: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(cap.SignatureDomain+string(raw))))}
	verified, err := cap.Verify(signed)
	if err != nil {
		t.Fatal(err)
	}
	return signed, verified.Digest
}
func TestSDKProviderVerifiedInstallOwnershipAndRevocation(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("SDK owner", "sdk-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other owner", "sdk-other@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, user.ID, "SDK Space")
	document, key := sdkInstallFixture(t, "example.habits")
	signed, digest := sdkSignFixture(t, document, key)
	if _, err := database.InstallVerifiedSDKApp(ctx, user.ID, signed, "wrong digest", space.ID); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("unreviewed install: %v", err)
	}
	if _, err := database.InstallVerifiedSDKApp(ctx, user.ID, signed, digest, space.ID); err != nil {
		t.Fatal(err)
	}
	tokenHash := security.HashToken("sdk-runtime-credential")
	session, err := database.CreateAppRuntimeSession(ctx, user.ID, document.AppID, tokenHash, space.ID, AppRuntimeSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	appctx := WithAppExecutionAuthority(ctx, *session)
	if _, err := database.InstallVerifiedSDKApp(appctx, user.ID, signed, digest, space.ID); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("self-install: %v", err)
	}
	provider := document.Capabilities.Providers[0]
	if _, err := database.RegisterSDKProvider(ctx, user.ID, digest, provider); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("missing app principal: %v", err)
	}
	if _, err := database.RegisterSDKProvider(appctx, other.ID, digest, provider); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("cross-user register: %v", err)
	}
	changed := provider
	changed.ID = "another.app/backend"
	if _, err := database.RegisterSDKProvider(appctx, user.ID, digest, changed); err == nil {
		t.Fatal("registered another app's provider")
	}
	if _, err := database.RegisterSDKProvider(appctx, user.ID, "bad digest", provider); !errors.Is(err, ErrSDKVersionConflict) {
		t.Fatalf("forged digest: %v", err)
	}
	var group sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := database.RegisterSDKProvider(appctx, user.ID, digest, provider)
			errs <- err
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := database.DiscoverSDKProviders(appctx, user.ID, SDKProviderDiscovery{})
	if err != nil || len(page.Providers) != 1 {
		t.Fatalf("discover: %#v %v", page, err)
	}
	page, err = database.DiscoverSDKProviders(ctx, other.ID, SDKProviderDiscovery{})
	if err != nil || len(page.Providers) != 0 {
		t.Fatalf("cross-user discover: %#v %v", page, err)
	}
	available := cap.Availability{State: "available", ObservedAt: time.Now().UTC()}
	if err := database.ReportSDKProviderAvailability(appctx, user.ID, provider.ID, available); err != nil {
		t.Fatal(err)
	}
	stale := available
	stale.ObservedAt = stale.ObservedAt.Add(-time.Second)
	if err := database.ReportSDKProviderAvailability(appctx, user.ID, provider.ID, stale); !errors.Is(err, ErrSDKProviderUnavailable) {
		t.Fatalf("stale observation: %v", err)
	}
	if err := database.UnregisterSDKProvider(appctx, user.ID, provider.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.ReportSDKProviderAvailability(appctx, user.ID, provider.ID, available); !errors.Is(err, ErrSDKProviderUnavailable) {
		t.Fatalf("availability revived unregistered provider: %v", err)
	}
	if _, err := database.RegisterSDKProvider(appctx, user.ID, digest, provider); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RemoveSpaceApp(ctx, user.ID, space.ID, document.AppID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RegisterSDKProvider(appctx, user.ID, digest, provider); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("register after uninstall: %v", err)
	}
	if _, err := database.InstallVerifiedSDKApp(ctx, user.ID, signed, digest, space.ID); err != nil {
		t.Fatal(err)
	}
	resolved, err := database.AppRuntimeSessionByToken(ctx, tokenHash)
	if err != nil || resolved != nil {
		t.Fatalf("reinstall revived old token: %#v %v", resolved, err)
	}
	page, err = database.DiscoverSDKProviders(ctx, user.ID, SDKProviderDiscovery{})
	if err != nil || len(page.Providers) != 0 {
		t.Fatalf("reinstall revived registration: %#v %v", page, err)
	}
}

func TestSDKProviderVersionsAndSemanticCollisions(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("SDK versions", "sdk-versions@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, user.ID, "SDK Space")
	document, key := sdkInstallFixture(t, "example.habits")
	signed, digest := sdkSignFixture(t, document, key)
	install := func(d cap.InstallDocument, key ed25519.PrivateKey) error {
		s, h := sdkSignFixture(t, d, key)
		_, err := database.InstallVerifiedSDKApp(ctx, user.ID, s, h, space.ID)
		return err
	}
	if _, err := database.InstallVerifiedSDKApp(ctx, user.ID, signed, digest, space.ID); err != nil {
		t.Fatal(err)
	}
	_, wrongKey := sdkInstallFixture(t, document.AppID)
	if err := install(document, wrongKey); !errors.Is(err, ErrSDKPublisherChanged) {
		t.Fatalf("publisher key changed: %v", err)
	}
	document.Capabilities.Providers[0].Label = "Changed label"
	if err := install(document, key); !errors.Is(err, ErrSDKVersionConflict) {
		t.Fatalf("immutable manifest overwritten: %v", err)
	}
	document.Version = "1.1.0"
	if err := install(document, key); !errors.Is(err, ErrSDKVersionConflict) {
		t.Fatalf("immutable provider overwritten: %v", err)
	}
	document.Capabilities.Providers[0].Version = 2
	if err := install(document, key); err != nil {
		t.Fatal(err)
	}
	peer, peerKey := sdkInstallFixture(t, "another.habits")
	if err := install(peer, peerKey); err != nil {
		t.Fatalf("same semantic contract under another provider: %v", err)
	}
	peer.Version = "2.0.0"
	peer.Capabilities.Providers[0].Version = 2
	peer.Capabilities.Providers[0].Capabilities[0].OutputSchema = json.RawMessage(`{"type":"boolean"}`)
	if err := install(peer, peerKey); !errors.Is(err, ErrSDKVersionConflict) {
		t.Fatalf("conflicting semantic contract accepted: %v", err)
	}
	apps, err := database.SpaceApps(ctx, user.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range apps {
		if app.AppID == peer.AppID && app.InstalledVersion != "1.0.0" {
			t.Fatal("failed install changed current version")
		}
	}
	peer.Capabilities.Providers[0].Capabilities[0].Version = 2
	if err := install(peer, peerKey); err != nil {
		t.Fatal(err)
	}
}

func TestSDKProviderDiscoveryGrantCeilingAndPagination(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("SDK discovery", "sdk-discovery@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, user.ID, "SDK Space")
	document, key := sdkInstallFixture(t, "example.habits")
	for _, id := range []string{"second", "third"} {
		provider := document.Capabilities.Providers[0]
		provider.ID = document.AppID + "/" + id
		document.Capabilities.Providers = append(document.Capabilities.Providers, provider)
	}
	signed, digest := sdkSignFixture(t, document, key)
	if _, err := database.InstallVerifiedSDKApp(ctx, user.ID, signed, digest, space.ID); err != nil {
		t.Fatal(err)
	}
	session, err := database.CreateAppRuntimeSession(ctx, user.ID, document.AppID, security.HashToken("sdk-paging"), space.ID, AppRuntimeSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	appctx := WithAppExecutionAuthority(ctx, *session)
	for _, provider := range document.Capabilities.Providers {
		if _, err := database.RegisterSDKProvider(appctx, user.ID, digest, provider); err != nil {
			t.Fatal(err)
		}
	}
	cursor := ""
	seen := map[string]bool{}
	for range 3 {
		page, err := database.DiscoverSDKProviders(appctx, user.ID, SDKProviderDiscovery{Limit: 1, Cursor: cursor})
		if err != nil || len(page.Providers) != 1 {
			t.Fatalf("page %#v %v", page, err)
		}
		if seen[page.Providers[0].ID] {
			t.Fatal("duplicate provider")
		}
		seen[page.Providers[0].ID] = true
		if page.NextCursor == nil {
			cursor = ""
		} else {
			cursor = *page.NextCursor
		}
	}
	if len(seen) != 3 || cursor != "" {
		t.Fatal("provider pagination hid results")
	}
	ceiling := *session
	ceiling.Scopes = []string{"capabilities.read"}
	page, err := database.DiscoverSDKProviders(WithAppExecutionAuthority(ctx, ceiling), user.ID, SDKProviderDiscovery{})
	if err != nil || len(page.Providers) != 0 {
		t.Fatalf("escaped admission ceiling: %#v %v", page, err)
	}
	if _, err := database.InstallSpaceApp(ctx, user.ID, space.ID, AppInstallSpec{ID: document.AppID, Version: document.Version, PermissionVersion: 2, Scopes: []string{"capabilities.read", "capabilities.providers.write"}}, nil); err != nil {
		t.Fatal(err)
	}
	page, err = database.DiscoverSDKProviders(appctx, user.ID, SDKProviderDiscovery{})
	if !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("escaped revoked current grants: %#v %v", page, err)
	}
	if _, err := database.RegisterSDKProvider(appctx, user.ID, digest, document.Capabilities.Providers[0]); err == nil {
		t.Fatal("registration restored revoked capability grant")
	}
}

func TestSDKProviderSpaceMemberUsesSharedVerifiedInstallation(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("SDK Space owner", "sdk-space-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("SDK Space member", "sdk-space-member@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Shared SDK")
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	document, key := sdkInstallFixture(t, "example.habits")
	signed, digest := sdkSignFixture(t, document, key)
	if _, err = database.InstallVerifiedSDKApp(ctx, owner.ID, signed, digest, space.ID); err != nil {
		t.Fatal(err)
	}
	session, err := database.CreateAppRuntimeSession(ctx, member.ID, document.AppID, security.HashToken("shared-sdk-member"), space.ID, AppRuntimeSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	memberCtx := WithAppExecutionAuthority(ctx, *session)
	if _, err = database.RegisterSDKProvider(memberCtx, member.ID, digest, document.Capabilities.Providers[0]); err != nil {
		t.Fatalf("member requires personal installation: %v", err)
	}
	if err = database.SetSpaceMemberPermission(ctx, owner.ID, space.ID, member.ID, PermissionAppsManage, "allow"); err != nil {
		t.Fatal(err)
	}
	_, otherKey := sdkInstallFixture(t, document.AppID)
	forged, forgedDigest := sdkSignFixture(t, document, otherKey)
	if _, err = database.InstallVerifiedSDKApp(ctx, member.ID, forged, forgedDigest, space.ID); !errors.Is(err, ErrSDKPublisherChanged) {
		t.Fatalf("manager replaced shared publisher: %v", err)
	}
	if _, err = database.InstallVerifiedSDKApp(ctx, member.ID, signed, digest, space.ID); err != nil {
		t.Fatalf("delegated manager cannot review existing release: %v", err)
	}
	if _, err = database.RegisterSDKProvider(memberCtx, member.ID, digest, document.Capabilities.Providers[0]); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("old generation remained live: %v", err)
	}
}
